package gatewayworkload

import kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"

func (k *Kubernetes) verifyInternalTLS(server object, id, namespace, host string) ([]byte, error) {
	return kube.VerifyServerTLSSecret(server, owner(id), kube.ServerTLSSecretTarget{
		Namespace: namespace, Name: "openshell-server-tls", DNSName: host, Roots: k.internalRoots,
	})
}
