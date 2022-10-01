package client

import (
	"context"
	"fmt"

	"github.com/faroshq/faros/pkg/models"
)

func (c *Client) ListClusterRegistrationTokens(ctx context.Context, token models.ClusterRegistrationToken) ([]models.ClusterRegistrationToken, error) {
	if token.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}

	var results []models.ClusterRegistrationToken
	if err := c.get(ctx, &results, namespacesURL, token.NamespaceID, clustersRegistrationTokenURL); err != nil {
		return nil, err
	}
	return results, nil
}

func (c *Client) CreateClusterRegistrationToken(ctx context.Context, token models.ClusterRegistrationToken) (*models.ClusterRegistrationToken, error) {
	if token.NamespaceID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}

	var result models.ClusterRegistrationToken
	if err := c.post(ctx, &token, result, namespacesURL, token.NamespaceID, clustersRegistrationTokenURL); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteClusterRegistrationToken(ctx context.Context, token models.ClusterRegistrationToken) error {
	if token.NamespaceID == "" {
		return fmt.Errorf("NamespaceID not selected")
	}

	if err := c.delete(ctx, namespacesURL, token.NamespaceID, clustersRegistrationTokenURL, token.ID); err != nil {
		return err
	}
	return nil
}
