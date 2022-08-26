package registry

import "github.com/faroshq/faros/pkg/models"

type ClusterRegistry interface {
	GetCluster(workspace, name string) (*models.Cluster, error)
	ListClusters(workspace string) ([]*models.Cluster, error)
	CreateCluster(workspace string, cluster *models.Cluster) error
	UpdateCluster(workspace string, cluster *models.Cluster) error
	DeleteCluster(workspace, name string) error
}
