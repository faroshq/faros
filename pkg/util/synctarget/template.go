package synctarget

import (
	"bytes"
	"embed"
	"html/template"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Copy from: github.com/kcp-dev/kcp/pkg/cliplugins/workload/plugin/sync.go

//go:embed *.yaml
var embeddedResources embed.FS

const (
	SyncerSecretConfigKey   = "kubeconfig"
	SyncerIDPrefix          = "faros-syncer-"
	DNSIDPrefix             = "faros-dns-"
	MaxSyncTargetNameLength = validation.DNS1123SubdomainMaxLength - (9 + len(SyncerIDPrefix))
)

// TemplateInput represents the external input required to render the resources to
// deploy the syncer to a pcluster.
type TemplateInput struct {
	// ServerURL is the logical cluster url the syncer configuration will use
	ServerURL string
	// CAData holds the PEM-encoded bytes of the ca certificate(s) a syncer will use to validate
	// kcp's serving certificate
	CAData string
	// Token is the service account token used to authenticate a syncer for access to a workspace
	Token string
	// KCPNamespace is the name of the kcp namespace of the syncer's service account
	KCPNamespace string
	// Namespace is the name of the syncer namespace on the pcluster
	Namespace string
	// SyncTargetPath is the qualified kcp logical cluster name the syncer will sync from
	SyncTargetPath string
	// SyncTarget is the name of the sync target the syncer will use to
	// communicate its status and read configuration from
	SyncTarget string
	// SyncTargetUID is the UID of the sync target the syncer will use to
	// communicate its status and read configuration from. This information is used by the
	// Syncer in order to avoid a conflict when a synctarget gets deleted and another one is
	// created with the same name.
	SyncTargetUID string
	// ResourcesToSync is the set of qualified resource names (eg. ["services",
	// "deployments.apps.k8s.io") that the syncer will synchronize between the kcp
	// workspace and the pcluster.
	ResourcesToSync []string
	// Image is the name of the container image that the syncer deployment will use
	Image string
	// Replicas is the number of syncer pods to run (should be 0 or 1).
	Replicas int
	// QPS is the qps the syncer uses when talking to an apiserver.
	QPS float32
	// Burst is the burst the syncer uses when talking to an apiserver.
	Burst int
	// FeatureGatesString is the set of features gates.
	FeatureGatesString string
	// APIImportPollIntervalString is the string of interval to poll APIImport.
	APIImportPollIntervalString string
	// DownstreamNamespaceCleanDelay is the time to delay before cleaning the downstream namespace as a string.
	DownstreamNamespaceCleanDelayString string
}

// templateArgs represents the full set of arguments required to render the resources
// required to deploy the syncer.
type templateArgs struct {
	TemplateInput
	// ServiceAccount is the name of the service account to create in the syncer
	// namespace on the pcluster.
	ServiceAccount string
	// ClusterRole is the name of the cluster role to create for the syncer on the
	// pcluster.
	ClusterRole string
	// ClusterRoleBinding is the name of the cluster role binding to create for the
	// syncer on the pcluster.
	ClusterRoleBinding string
	// DnsRole is the name of the DNS role to create for the syncer on the pcluster.
	DNSRole string
	// DNSRoleBinding is the name of the DNS role binding to create for the
	// syncer on the pcluster.
	DNSRoleBinding string
	// GroupMappings is the mapping of api group to resources that will be used to
	// define the cluster role rules for the syncer in the pcluster. The syncer will be
	// granted full permissions for the resources it will synchronize.
	GroupMappings []GroupMapping
	// Secret is the name of the secret that will contain the kubeconfig the syncer
	// will use to connect to the kcp logical cluster (workspace) that it will
	// synchronize from.
	Secret string
	// Key in the syncer secret for the kcp logical cluster kubconfig.
	SecretConfigKey string
	// Deployment is the name of the deployment that will run the syncer in the
	// pcluster.
	Deployment string
	// DeploymentApp is the label value that the syncer's deployment will select its
	// pods with.
	DeploymentApp string
}

// groupMapping associates an api group to the resources in that group.
type GroupMapping struct {
	APIGroup  string
	Resources []string
}

// RenderSyncerResources renders the resources required to deploy a syncer to a pcluster.
func RenderSyncerResources(input TemplateInput, syncerID string, resourceForPermission []string) ([]byte, error) {
	dnsSyncerID := strings.Replace(syncerID, "syncer", "dns", 1)

	tmplArgs := templateArgs{
		TemplateInput:      input,
		ServiceAccount:     syncerID,
		ClusterRole:        syncerID,
		ClusterRoleBinding: syncerID,
		DNSRole:            dnsSyncerID,
		DNSRoleBinding:     dnsSyncerID,
		GroupMappings:      GetGroupMappings(resourceForPermission),
		Secret:             syncerID,
		SecretConfigKey:    SyncerSecretConfigKey,
		Deployment:         syncerID,
		DeploymentApp:      syncerID,
	}

	syncerTemplate, err := embeddedResources.ReadFile("syncer.yaml")
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New("syncerTemplate").Parse(string(syncerTemplate))
	if err != nil {
		return nil, err
	}
	buffer := bytes.NewBuffer([]byte{})
	err = tmpl.Execute(buffer, tmplArgs)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// GetGroupMappings returns the set of api groups to resources for the given resources.
func GetGroupMappings(resourcesToSync []string) []GroupMapping {
	groupMap := make(map[string][]string)

	for _, resource := range resourcesToSync {
		nameParts := strings.SplitN(resource, ".", 2)
		name := nameParts[0]
		apiGroup := ""
		if len(nameParts) > 1 {
			apiGroup = nameParts[1]
		}
		if _, ok := groupMap[apiGroup]; !ok {
			groupMap[apiGroup] = []string{name}
		} else {
			groupMap[apiGroup] = append(groupMap[apiGroup], name)
		}
		// If pods are being synced, add the subresources that are required to
		// support the pod subresources.
		if apiGroup == "" && name == "pods" {
			podSubresources := []string{
				"pods/log",
				"pods/exec",
				"pods/attach",
				"pods/binding",
				"pods/portforward",
				"pods/proxy",
				"pods/ephemeralcontainers",
			}
			groupMap[apiGroup] = append(groupMap[apiGroup], podSubresources...)
		}
	}

	groupMappings := make([]GroupMapping, 0, len(groupMap))
	for apiGroup, resources := range groupMap {
		groupMappings = append(groupMappings, GroupMapping{
			APIGroup:  apiGroup,
			Resources: resources,
		})
	}

	sortGroupMappings(groupMappings)

	return groupMappings
}

// sortGroupMappings sorts group mappings first by APIGroup and then by Resources.
func sortGroupMappings(groupMappings []GroupMapping) {
	sort.Slice(groupMappings, func(i, j int) bool {
		if groupMappings[i].APIGroup == groupMappings[j].APIGroup {
			return strings.Join(groupMappings[i].Resources, ",") < strings.Join(groupMappings[j].Resources, ",")
		}
		return groupMappings[i].APIGroup < groupMappings[j].APIGroup
	})
}
