package kubeconfig

import "fmt"

func GetClusterConnectURL(host, namespaceID, clusterID, accessID string) string {
	return fmt.Sprintf("%s/namespaces/%s/clusters/%s/access/%s/connect}", host, namespaceID, clusterID, accessID)
}
