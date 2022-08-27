package client

import (
	"context"
	"fmt"

	"github.com/faroshq/faros/pkg/models"
)

func (c *Client) GetCluster(ctx context.Context, cluster models.Cluster) (*models.Cluster, error) {
	if cluster.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}
	if cluster.ID == "" {
		return nil, fmt.Errorf("clusterID not provided")
	}

	var result models.Cluster
	if err := c.get(ctx, &result, namespacesURL, cluster.NamespaceID, clustersURL, cluster.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListClusters(ctx context.Context, cluster models.Cluster) ([]models.Cluster, error) {
	if cluster.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}

	var results []models.Cluster
	if err := c.get(ctx, &results, namespacesURL, cluster.NamespaceID, clustersURL); err != nil {
		return nil, err
	}
	return results, nil
}

func (c *Client) CreateCluster(ctx context.Context, cluster models.Cluster) (*models.Cluster, error) {
	if cluster.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}
	if cluster.Name == "" {
		return nil, fmt.Errorf("cluster name not provided")
	}
	if cluster.Config.RawKubeConfig == "" {
		return nil, fmt.Errorf("kubeconfig not provided")
	}

	var result models.Cluster
	if err := c.post(ctx, &cluster, cluster, namespacesURL, cluster.NamespaceID, clustersURL); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpdateCluster(ctx context.Context, cluster models.Cluster) (*models.Cluster, error) {
	if cluster.NamespaceID == "" {
		return nil, fmt.Errorf("NamespaceID not selected")
	}
	if cluster.Name == "" {
		return nil, fmt.Errorf("cluster name not provided")
	}
	if cluster.Config.RawKubeConfig == "" {
		return nil, fmt.Errorf("kubeconfig not provided")
	}

	var result models.Cluster
	if err := c.post(ctx, &cluster, cluster, namespacesURL, cluster.NamespaceID, clustersURL, cluster.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteCluster(ctx context.Context, cluster models.Cluster) error {
	if cluster.NamespaceID == "" {
		return fmt.Errorf("NamespaceID not selected")
	}
	if cluster.ID == "" {
		return fmt.Errorf("cluster name not provided")
	}

	if err := c.delete(ctx, namespacesURL, cluster.NamespaceID, clustersURL, cluster.ID); err != nil {
		return err
	}
	return nil
}
