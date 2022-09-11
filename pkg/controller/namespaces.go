package controller

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
)

func (c *controller) ListNamespaces(ctx context.Context) ([]models.Namespace, error) {
	return c.store.ListNamespaces(ctx)
}

func (c *controller) GetNamespace(ctx context.Context, namespaceID string) (*models.Namespace, error) {
	query := models.Namespace{
		ID: namespaceID,
	}
	return c.store.GetNamespace(ctx, query)
}

func (c *controller) CreateNamespace(ctx context.Context, model models.Namespace) (*models.Namespace, error) {
	return c.store.CreateNamespace(ctx, model)
}

func (c *controller) DeleteNamespace(ctx context.Context, namespaceID string) error {
	return c.store.DeleteNamespace(ctx, models.Namespace{
		ID: namespaceID,
	})
}

func (c *controller) UpdateNamespace(ctx context.Context, model models.Namespace) (*models.Namespace, error) {
	return c.store.UpdateNamespace(ctx, model)
}
