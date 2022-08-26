package namespaces

import (
	"context"
	"fmt"
	"strings"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
)

func delete(ctx context.Context, args []string) error {
	c := &config.Config

	if len(args) == 0 {
		return fmt.Errorf("namespace name argument missing")
	}

	namespaces, err := c.APIClient.ListNamespaces(ctx)
	if err != nil {
		return errors.ParseCloudError(err)
	}
	if len(namespaces) == 0 {
		return fmt.Errorf("no namespaces found")
	}

	for _, arg := range args {

		for _, namespace := range namespaces {
			if strings.EqualFold(namespace.Name, arg) {
				err := c.APIClient.DeleteNamespace(ctx, namespace)
				if err != nil {
					return errors.ParseCloudError(err)
				}
				fmt.Printf("Namespace %s deleted\n", arg)
			}
		}
	}
	return nil
}
