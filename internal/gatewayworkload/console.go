package gatewayworkload

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"

	deployment "github.com/jsell-rh/hypershell-stego/gateway-console/out/deploy"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const consoleName = "hypershell-gateway-console"
const consoleComponentLabel = "hypershell.redhat.io/component"

var consoleSecretNames = [...]string{consoleName + "-runtime", consoleName + "-files", "dashboard-application-env", "dashboard-application-files"}

func consoleOwner(id string) kube.Owner {
	return kube.Owner{ownerLabel: id, consoleComponentLabel: "gateway-console"}
}

// consoleResources uses the separately generated browser module. The allocator
// owns network policy. No Gateway Service selector label can enter these Pods.
func consoleResources(gw *pb.Gateway, image string, group uint64, digest string) ([]resource, error) {
	ns, err := Namespace(gw.GetMetadata().GetId())
	if err != nil || ns != gw.GetNamespace() {
		return nil, errors.New("console placement does not match its Gateway")
	}
	raw, err := hex.DecodeString(digest)
	if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != digest {
		return nil, errors.New("console configuration digest is invalid")
	}
	rendered, err := deployment.Resources(deployment.Options{Image: image, Namespace: ns, FSGroup: group, Scope: "namespace", OwnerLabels: consoleOwner(gw.Metadata.Id)})
	if err != nil {
		return nil, err
	}
	result := []resource{}
	seen := map[string]bool{}
	for _, entry := range rendered {
		if seen[entry.Kind] {
			return nil, errors.New("console deployment repeats a resource kind")
		}
		seen[entry.Kind] = true
		switch entry.Kind {
		case "NetworkPolicy":
			continue
		case "ServiceAccount", "Service":
		case "Deployment":
			spec, _ := entry.Object["spec"].(map[string]any)
			template, _ := spec["template"].(map[string]any)
			meta, _ := template["metadata"].(map[string]any)
			labels, _ := meta["labels"].(map[string]any)
			if meta == nil || labels == nil {
				return nil, errors.New("console deployment has no Pod identity")
			}
			if _, present := labels[ownerLabel]; present {
				return nil, errors.New("console Pods must not match the Gateway Service")
			}
			annotations, _ := meta["annotations"].(map[string]any)
			if annotations == nil {
				annotations = map[string]any{}
				meta["annotations"] = annotations
			}
			annotations["hypershell.redhat.io/console-configuration"] = digest
		default:
			return nil, errors.New("console deployment has an unsupported resource")
		}
		result = append(result, resource{path: entry.Collection, object: entry.Object})
	}
	if len(result) != 3 || !seen["NetworkPolicy"] || !seen["Deployment"] || !seen["Service"] || !seen["ServiceAccount"] {
		return nil, errors.New("console deployment is incomplete")
	}
	return result, nil
}

// A rollout depends on credential contents, not Secret metadata updates.
func consoleConfigurationDigest(id string, secrets []object) (string, error) {
	ns, err := Namespace(id)
	if err != nil {
		return "", err
	}
	return kube.OpaqueSecretSetDigest(ns, consoleSecretNames[:], consoleOwner(id), secrets)
}

// EnsureConsole starts only after the allocator and all four controller-owned
// dependency Secrets are ready. The caller must provision durable state first.
func (k *Kubernetes) EnsureConsole(ctx context.Context, gw *pb.Gateway, image string, group uint64) error {
	if !k.Handles(gw) || k.allocation == nil {
		return errors.New("console requires its assigned namespace allocator")
	}
	id, ns := gw.GetMetadata().GetId(), gw.GetNamespace()
	expected, err := Namespace(id)
	if err != nil || ns != expected {
		return errors.New("console placement is invalid")
	}
	if err := k.allocation.RequireNamespace(ctx, "gateway", ns, id); err != nil {
		if errors.Is(err, allocation.ErrPending) {
			return ErrPending
		}
		return err
	}
	dependencies := make([]object, 0, len(consoleSecretNames))
	for _, name := range consoleSecretNames {
		secret, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns+"/secrets/"+name, nil)
		if err != nil {
			return err
		}
		if code == http.StatusNotFound {
			return ErrPending
		}
		if code != http.StatusOK {
			return errors.New("console dependency read failed")
		}
		dependencies = append(dependencies, secret)
	}
	digest, err := consoleConfigurationDigest(id, dependencies)
	if err != nil {
		return err
	}
	entries, err := consoleResources(gw, image, group, digest)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := k.client.Ensure(ctx, entry.path, entry.object, consoleOwner(id)); err != nil {
			return err
		}
	}
	current, code, err := k.client.Request(ctx, http.MethodGet, "/apis/apps/v1/namespaces/"+ns+"/deployments/"+consoleName, nil)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		return ErrPending
	}
	if code != http.StatusOK {
		return errors.New("console deployment read failed")
	}
	ready, err := kube.DeploymentAvailable(current, consoleOwner(id), 1)
	if err != nil {
		return err
	}
	if !ready {
		return ErrPending
	}
	return nil
}
