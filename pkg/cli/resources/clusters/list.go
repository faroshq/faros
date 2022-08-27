package clusters

import (
	"context"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/cli/util/formater"
	printutil "github.com/faroshq/faros/pkg/cli/util/print"
	"github.com/faroshq/faros/pkg/models"
)

func list(ctx context.Context) error {
	c := &config.Config

	namespaces, err := c.APIClient.ListNamespaces(ctx)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	clusters, err := c.APIClient.ListClusters(ctx, models.Cluster{NamespaceID: c.Namespace})
	if err != nil {
		return errors.ParseCloudError(err)
	}

	var clusterList []struct {
		models.Cluster
		Namespace string
	}

	for _, cluster := range clusters {
		for _, namespace := range namespaces {
			if cluster.NamespaceID == namespace.ID {
				clusterList = append(clusterList, struct {
					models.Cluster
					Namespace string
				}{
					cluster,
					namespace.Name,
				})
			}
		}
	}

	if c.Output == printutil.FormatTable {
		table := printutil.DefaultTable()
		table.SetHeader([]string{"Name", "Namespace", "Created", "Updated"})
		for _, c := range clusterList {
			createdStr := formater.Since(c.CreatedAt).String() + " ago"
			updatedStr := formater.Since(c.UpdatedAt).String() + " ago"

			table.Append([]string{
				c.Name,
				c.Namespace,
				createdStr,
				updatedStr,
			})
		}

		table.Render()
		return nil
	}

	return printutil.PrintWithFormat(clusterList, c.Output)
}
