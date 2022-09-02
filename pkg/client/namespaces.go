package client

import (
	"context"
	"fmt"

	"github.com/faroshq/faros/pkg/models"
)

func (c *Client) GetNamespace(ctx context.Context, namespace models.Namespace) (*models.Namespace, error) {
	if namespace.ID == "" {
		return nil, fmt.Errorf("namespaceID not provided")
	}

	var result models.Namespace
	if err := c.get(ctx, &result, namespacesURL, namespace.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListNamespaces(ctx context.Context) ([]models.Namespace, error) {
	var result []models.Namespace
	if err := c.get(ctx, &result, namespacesURL); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) CreateNamespace(ctx context.Context, namespace models.Namespace) (*models.Namespace, error) {
	if namespace.Name == "" {
		return nil, fmt.Errorf("namespace name not provided")
	}
	if namespace.Name == "" {
		return nil, fmt.Errorf("namespace name not provided")
	}

	var result models.Namespace
	if err := c.post(ctx, namespace, &result, namespacesURL); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UpdateNamespace(ctx context.Context, namespace models.Namespace) (*models.Namespace, error) {
	if namespace.ID == "" {
		return nil, fmt.Errorf("namespaceID name not provided")
	}

	var result models.Namespace
	if err := c.post(ctx, &result, namespace, namespacesURL, namespace.ID); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteNamespace(ctx context.Context, cluster models.Namespace) error {
	if cluster.ID == "" {
		return fmt.Errorf("NamespaceID not selected")
	}

	if err := c.delete(ctx, namespacesURL, cluster.ID); err != nil {
		return err
	}
	return nil
}
