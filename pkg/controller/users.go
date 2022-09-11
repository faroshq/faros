package controller

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
)

func (c *controller) GetUser(ctx context.Context, query models.User) (*models.User, error) {
	return c.store.GetUser(ctx, query)
}

func (c *controller) CreateUser(ctx context.Context, query models.User) (*models.User, error) {
	return c.store.CreateUser(ctx, query)
}

func (c *controller) ListUsers(ctx context.Context, query models.User) ([]models.User, error) {
	return c.store.ListUsers(ctx, query)
}

func (c *controller) DeleteUser(ctx context.Context, query models.User) error {
	return c.store.DeleteUser(ctx, query)
}
