package controller

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
)

func (c *controller) GetCluster(ctx context.Context, namespaceID, clusterID string) (*models.Cluster, error) {
	clusterQuery := models.Cluster{
		NamespaceID: namespaceID,
		ID:          clusterID,
	}

	return c.store.GetCluster(ctx, clusterQuery)
}

func (c *controller) CreateCluster(ctx context.Context, model models.Cluster) (*models.Cluster, error) {
	return c.store.CreateCluster(ctx, model)
}

func (c *controller) DeleteCluster(ctx context.Context, clusterID string) error {
	return c.store.DeleteCluster(ctx, models.Cluster{
		ID: clusterID,
	})
}

func (c *controller) ListClusters(ctx context.Context, namespaceID string) ([]models.Cluster, error) {
	query := models.Cluster{
		NamespaceID: namespaceID,
	}
	return c.store.ListClusters(ctx, query)
}

func (c *controller) UpdateCluster(ctx context.Context, model models.Cluster) (*models.Cluster, error) {
	return c.store.UpdateCluster(ctx, model)
}
