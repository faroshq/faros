package workload

const (
	FarosConfigMapName      = "faros-config"
	FarosConfigMapNamespace = "default"
	FarosConfigMapServerKey = "server"
	// FarosConfigMapLabelName is the label name for the faros configmap stating
	// the name of the sync target
	FarosConfigMapLabelName               = "synctarget.workload.faros.sh/name"
	FarosConfigMapLabelBootstrapBootstrap = "synctarget.workload.faros.sh/bootstrap"

	// SycnTargetAnnotationBootstrap is the annotation name for the sync target
	// stating whether it is required to bootstrap the sync target
	SyncTargetAnnotationBootstrap = "synctarget.workload.faros.sh/bootstrap"
)
