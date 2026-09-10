package databasecontroller

import (
	"context"
	"errors"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const CNPGClusterName = "openshell-db"
const CNPGPostgresImage = "ghcr.io/cloudnative-pg/postgresql:18.6-minimal-trixie@sha256:d67f21ed2110b1f59349217760e389f1bded54cd0b6dee485ed152b4c8a8b9e2"
const cnpgAPI = "/apis/postgresql.cnpg.io/v1"

// CNPG supplies the shared database Cluster. The operator owns its Pods,
// certificates, and volumes. Gateway database and role policy is separate.
type CNPG struct{ client *kube.Client }

func NewCNPG(o KubernetesOptions) (*CNPG, error) {
	c, err := kube.New(kube.Options{ServerURL: o.ServerURL, CAFile: o.CAFile, TokenFile: o.TokenFile})
	if err != nil {
		return nil, err
	}
	return &CNPG{client: c}, nil
}
func (c *CNPG) Close() { c.client.Close() }

func validateCNPGPlacement(db *pb.ManagedDatabase) error {
	if db == nil || db.GetProvider() != gateways.ProviderCNPG {
		return errors.New("CNPG database placement is required")
	}
	ns, err := gateways.DatabaseNamespace(db.GetMetadata().GetId())
	if err != nil || db.GetNamespace() != ns {
		return errors.New("database namespace does not match its ID")
	}
	return nil
}
func validateCNPG(db *pb.ManagedDatabase) error {
	if err := validateCNPGPlacement(db); err != nil {
		return err
	}
	switch db.GetEngine() {
	case "", "postgres", "postgresql":
	default:
		return errors.New("CNPG database engine is not supported")
	}
	switch db.GetEngineVersion() {
	case "", "18", "18.6":
	default:
		return errors.New("CNPG database engine version is not supported")
	}
	if db.GetRegion() != "" || db.GetInstanceClass() != "" || db.GetConnectionSecret() != "" {
		return errors.New("CNPG database region, instance class, and connection Secret overrides are not supported")
	}
	return nil
}

// Require all three APIs from the reference provider contract before effects.
// API discovery does not replace the authorization check on each operation.
func (c *CNPG) requireAPI(ctx context.Context) error {
	return c.client.RequireResources(ctx, "postgresql.cnpg.io/v1",
		kube.APIResource{Name: "clusters", Kind: "Cluster", Namespaced: true, Verbs: []string{"get", "create", "patch", "delete"}},
		kube.APIResource{Name: "databases", Kind: "Database", Namespaced: true, Verbs: []string{"get", "create", "patch", "delete"}},
		kube.APIResource{Name: "databaseroles", Kind: "DatabaseRole", Namespaced: true, Verbs: []string{"get", "create", "patch", "delete"}},
	)
}

func cnpgDefinition(id string) object {
	cluster := definition("postgresql.cnpg.io/v1", "Cluster", CNPGClusterName, id)
	cluster["spec"] = object{
		"instances": 1, "imageName": CNPGPostgresImage, "enableSuperuserAccess": false,
		"storage":    object{"size": "1Gi"},
		"resources":  object{"requests": object{"cpu": "100m", "memory": "256Mi"}, "limits": object{"cpu": "1", "memory": "512Mi"}},
		"bootstrap":  object{"initdb": object{"database": "openshell", "owner": "openshell", "dataChecksums": true}},
		"postgresql": object{"parameters": object{"password_encryption": "scram-sha-256"}, "pg_hba": []string{"hostnossl all all all reject", "hostssl sameuser all all scram-sha-256", "hostssl all all all reject"}},
	}
	return cluster
}
func (c *CNPG) Ensure(ctx context.Context, db *pb.ManagedDatabase) error {
	if err := validateCNPG(db); err != nil {
		return err
	}
	if err := c.requireAPI(ctx); err != nil {
		return err
	}
	id, ns := db.GetMetadata().GetId(), db.GetNamespace()
	namespace := definition("v1", "Namespace", ns, id)
	namespace["metadata"].(object)["labels"].(object)["pod-security.kubernetes.io/enforce"] = "restricted"
	if _, err := c.client.Ensure(ctx, "/api/v1/namespaces", namespace, owner(id)); err != nil {
		return err
	}
	// Always repair the desired object, including after a prior ready observation.
	cluster, err := c.client.Ensure(ctx, cnpgAPI+"/namespaces/"+ns+"/clusters", cnpgDefinition(id), owner(id))
	if err != nil {
		return err
	}
	if str(cluster, "metadata", "uid") == "" || integer(cluster, "metadata", "generation") < 1 || integer(cluster, "status", "instances") != 1 || integer(cluster, "status", "readyInstances") != 1 || str(cluster, "status", "phase") != "Cluster in healthy state" || str(cluster, "status", "image") != CNPGPostgresImage {
		return ErrPending
	}
	primary := str(cluster, "status", "currentPrimary")
	if !dnsName.MatchString(primary) || primary != str(cluster, "status", "targetPrimary") {
		return ErrPending
	}
	pod, code, err := c.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns+"/pods/"+primary, nil)
	if err != nil {
		return err
	}
	if code == 404 || !cnpgPodReady(pod, str(cluster, "metadata", "uid")) {
		return ErrPending
	}
	return nil
}

func cnpgPodReady(pod object, uid string) bool {
	if uid == "" || str(pod, "metadata", "deletionTimestamp") != "" || str(pod, "status", "phase") != "Running" {
		return false
	}
	refs, _ := nested(pod, "metadata", "ownerReferences").([]any)
	owns := false
	for _, value := range refs {
		ref, _ := value.(map[string]any)
		if str(ref, "apiVersion") == "postgresql.cnpg.io/v1" && str(ref, "kind") == "Cluster" && str(ref, "name") == CNPGClusterName && str(ref, "uid") == uid && ref["controller"] == true {
			owns = true
		}
	}
	if !owns {
		return false
	}
	containers, _ := nested(pod, "spec", "containers").([]any)
	image := false
	for _, value := range containers {
		container, _ := value.(map[string]any)
		if str(container, "name") == "postgres" && str(container, "image") == CNPGPostgresImage && contains(container, object{"resources": nested(cnpgDefinition(""), "spec", "resources")}) {
			image = true
		}
	}
	conditions, _ := nested(pod, "status", "conditions").([]any)
	ready := false
	for _, value := range conditions {
		condition, _ := value.(map[string]any)
		if str(condition, "type") == "Ready" && str(condition, "status") == "True" {
			ready = true
		}
	}
	statuses, _ := nested(pod, "status", "containerStatuses").([]any)
	running := false
	for _, value := range statuses {
		status, _ := value.(map[string]any)
		if str(status, "name") == "postgres" && status["ready"] == true && nested(status, "state", "running") != nil {
			running = true
		}
	}
	return image && ready && running
}

func (c *CNPG) Delete(ctx context.Context, db *pb.ManagedDatabase) error {
	// Unsupported mutable settings cannot prevent cleanup of a valid placement.
	if err := validateCNPGPlacement(db); err != nil {
		return err
	}
	path := "/api/v1/namespaces/" + db.GetNamespace()
	namespace, code, err := c.client.Request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if code == 404 {
		return nil
	}
	if !owner(db.GetMetadata().GetId()).Matches(namespace) {
		return errors.New("CNPG namespace has a different owner")
	}
	if err := c.requireAPI(ctx); err != nil {
		return err
	}
	// Check Cluster ownership before deleting the containing namespace.
	gone, err := c.client.DeleteOwned(ctx, cnpgAPI+"/namespaces/"+db.GetNamespace()+"/clusters/"+CNPGClusterName, owner(db.GetMetadata().GetId()))
	if err != nil {
		return err
	}
	if !gone {
		return ErrPending
	}
	gone, err = c.client.DeleteOwned(ctx, path, owner(db.GetMetadata().GetId()))
	if err != nil {
		return err
	}
	if !gone {
		return ErrPending
	}
	return nil
}
