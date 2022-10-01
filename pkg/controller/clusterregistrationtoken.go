package controller

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
)

func (c *controller) GetClusterRegistrationToken(ctx context.Context, query models.ClusterRegistrationToken) (*models.ClusterRegistrationToken, error) {
	return c.store.GetClusterRegistrationToken(ctx, query)
}

func (c *controller) ListRegistrationToken(ctx context.Context, namespaceID string) ([]models.ClusterRegistrationToken, error) {
	clusterRegistrationTokenQuery := models.ClusterRegistrationToken{
		NamespaceID: namespaceID,
	}

	return c.store.ListRegistrationTokens(ctx, clusterRegistrationTokenQuery)
}

func (c *controller) DeleteRegistrationToken(ctx context.Context, tokenID string) error {
	clusterRegistrationTokenQuery := models.ClusterRegistrationToken{
		ID: tokenID,
	}

	return c.store.DeleteRegistrationToken(ctx, clusterRegistrationTokenQuery)
}

func (c *controller) CreateRegistrationToken(ctx context.Context, createClusterRegistrationTokenRequest models.ClusterRegistrationToken) (*models.ClusterRegistrationToken, error) {
	return c.store.CreateRegistrationToken(ctx, createClusterRegistrationTokenRequest)
}
