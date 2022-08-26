package namespaces

import (
	"context"
	"fmt"
	"strings"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/cli/util/formater"
	printutil "github.com/faroshq/faros/pkg/cli/util/print"
)

func list(ctx context.Context) error {
	c := &config.Config

	namespaces, err := c.APIClient.ListNamespaces(ctx)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	if c.Output == printutil.FormatTable {
		table := printutil.DefaultTable()
		table.SetHeader([]string{"Name", "ID", "Created", "Updated"})
		for _, w := range namespaces {
			createdStr := formater.Since(w.CreatedAt).String() + " ago"
			updatedStr := formater.Since(w.UpdatedAt).String() + " ago"

			table.Append([]string{
				w.Name,
				w.ID,
				createdStr,
				updatedStr,
			})
		}

		table.Render()
		return nil
	}

	return printutil.PrintWithFormat(namespaces, c.Output)
}

func get(ctx context.Context, args []string) error {
	c := &config.Config

	namespaces, err := c.APIClient.ListNamespaces(ctx)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	if len(namespaces) == 0 {
		return fmt.Errorf("no namespaces found")
	}

	for _, namespace := range namespaces {
		if strings.EqualFold(namespace.Name, args[0]) {
			return printutil.PrintWithFormat(namespace, printutil.OverrideTable(c.Output))
		}
	}
	return fmt.Errorf("namespace %s not found", args[0])
}
