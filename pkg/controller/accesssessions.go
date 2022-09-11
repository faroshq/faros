package controller

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
)

func (c *controller) GetClusterAccessSession(ctx context.Context, query models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	return c.store.GetClusterAccessSession(ctx, query)
}

func (c *controller) ListClusterAccessSessions(ctx context.Context, namespaceID, clusterID string) ([]models.ClusterAccessSession, error) {
	clusterAccessSessionQuery := models.ClusterAccessSession{
		NamespaceID: namespaceID,
		ClusterID:   clusterID,
	}

	return c.store.ListClusterAccessSessions(ctx, clusterAccessSessionQuery)
}

func (c *controller) DeleteClusterAccessSessions(ctx context.Context, sessionID string) error {
	clusterAccessSessionQuery := models.ClusterAccessSession{
		ID: sessionID,
	}

	return c.store.DeleteClusterAccessSession(ctx, clusterAccessSessionQuery)
}

func (c *controller) UpdateClusterAccessSession(ctx context.Context, session models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	return c.store.UpdateClusterAccessSession(ctx, session)
}

func (c *controller) CreateClusterAccessSession(ctx context.Context, createClusterAccessSessionRequest models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	return c.store.CreateClusterAccessSession(ctx, createClusterAccessSessionRequest)
}
