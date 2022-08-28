package namespaces

import (
	"context"
	"fmt"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/models"
)

func create(ctx context.Context, args []string) error {
	c := &config.Config

	for _, arg := range args {
		namespaceName := arg
		if namespaceName == "" {
			namespaceName = c.Namespace
		}

		namespace := &models.Namespace{}
		namespace.Name = namespaceName

		namespaces, err := c.APIClient.ListNamespaces(ctx)
		if err != nil {
			return errors.ParseCloudError(err)
		}

		for _, namespace := range namespaces {
			if namespace.Name == namespaceName {
				return fmt.Errorf("namespace %s already exists", namespaceName)
			}
		}

		result, err := c.APIClient.CreateNamespace(ctx, *namespace)
		if err != nil {
			return errors.ParseCloudError(err)
		}

		fmt.Printf("Namespace %s successfully created at %s!\n", result.Name, result.CreatedAt.Local().Format("Mon Jan _2 15:04:05 2006"))
	}
	return nil
}
