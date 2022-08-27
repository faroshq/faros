package client

import (
	"context"
	"fmt"

	"github.com/faroshq/faros/pkg/models"
)

func (c *Client) GetClusterAccessSession(ctx context.Context, access models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	if access.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}
	if access.ID == "" {
		return nil, fmt.Errorf("clusterID not provided")
	}

	var result models.ClusterAccessSession
	if err := c.get(ctx, &result, namespacesURL, access.NamespaceID, clustersURL, access.ID, accessURL, access.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListClusterAccessSessions(ctx context.Context, access models.ClusterAccessSession) ([]models.ClusterAccessSession, error) {
	if access.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}
	if access.ClusterID == "" {
		return nil, fmt.Errorf("clusterID not provided")
	}

	var results []models.ClusterAccessSession
	if err := c.get(ctx, &results, namespacesURL, access.NamespaceID, clustersURL, access.ClusterID, accessURL); err != nil {
		return nil, err
	}
	return results, nil
}

func (c *Client) CreateClusterAccessSession(ctx context.Context, access models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	if access.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}
	if access.ClusterID == "" {
		return nil, fmt.Errorf("clusterID not provided")
	}
	if access.TTL == 0 {
		return nil, fmt.Errorf("TTL not provided")
	}

	var result models.ClusterAccessSession
	if err := c.post(ctx, &access, result, namespacesURL, access.NamespaceID, clustersURL, access.ClusterID, accessURL); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpdateClusterAccessSession(ctx context.Context, access models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	if access.NamespaceID == "" {
		return nil, fmt.Errorf("NamespaceID not selected")
	}
	if access.ClusterID == "" {
		return nil, fmt.Errorf("clusterID not provided")
	}
	if access.ID == "" {
		return nil, fmt.Errorf("accessID not provided")
	}
	if access.TTL == 0 {
		return nil, fmt.Errorf("TTL not provided")
	}

	var result models.ClusterAccessSession
	if err := c.post(ctx, &access, result, namespacesURL, access.NamespaceID, clustersURL, access.ClusterID, accessURL, access.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteClusterAccessSession(ctx context.Context, access models.ClusterAccessSession) error {
	if access.NamespaceID == "" {
		return fmt.Errorf("NamespaceID not selected")
	}
	if access.ID == "" {
		return fmt.Errorf("accessID not provided")
	}
	if access.ClusterID == "" {
		return fmt.Errorf("clusterID not provided")
	}

	if err := c.delete(ctx, namespacesURL, access.NamespaceID, clustersURL, access.ClusterID, accessURL, access.ID); err != nil {
		return err
	}
	return nil
}

func (c *Client) CreateClusterAccessSessionKubeConfig(ctx context.Context, access models.ClusterAccessSession) (*models.KubeConfig, error) {
	if access.NamespaceID == "" {
		return nil, fmt.Errorf("NamespaceID not selected")
	}
	if access.ID == "" {
		return nil, fmt.Errorf("accessID not provided")
	}
	if access.ClusterID == "" {
		return nil, fmt.Errorf("clusterID not provided")
	}

	var result models.KubeConfig
	if err := c.post(ctx, nil, &result, namespacesURL, access.NamespaceID, clustersURL, access.ClusterID, accessURL, access.ID, kubeconfigURL); err != nil {
		return nil, err
	}
	return &result, nil
}
