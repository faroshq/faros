package access

import (
	"context"
	"fmt"
	"strconv"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/cli/util/formater"
	printutil "github.com/faroshq/faros/pkg/cli/util/print"
	"github.com/faroshq/faros/pkg/models"
)

func list(ctx context.Context, opts opts) error {
	c := &config.Config

	if opts.cluster == "" {
		return fmt.Errorf("cluster name flag missing")
	}

	clusterID, err := config.ResolveClusterFlag(ctx, opts.cluster)
	if err != nil {
		return err
	}

	namespaces, err := c.APIClient.ListNamespaces(ctx)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	sessions, err := c.APIClient.ListClusterAccessSessions(ctx, models.ClusterAccessSession{
		NamespaceID: c.Namespace,
		ClusterID:   clusterID,
	})
	if err != nil {
		return errors.ParseCloudError(err)
	}

	var sessionsList []struct {
		models.ClusterAccessSession
		ClusterName string
		Namespace   string
	}

	for _, session := range sessions {
		for _, namespace := range namespaces {
			if session.NamespaceID == namespace.ID {
				sessionsList = append(sessionsList, struct {
					models.ClusterAccessSession
					ClusterName string
					Namespace   string
				}{
					session,
					opts.cluster,
					namespace.Name,
				})
			}
		}
	}

	if c.Output == printutil.FormatTable {
		table := printutil.DefaultTable()
		table.SetHeader([]string{"Name", "Cluster", "Namespace", "Created", "Updated", "TTL", "Expired"})
		for _, c := range sessionsList {
			createdStr := formater.Since(c.CreatedAt).String() + " ago"
			updatedStr := formater.Since(c.UpdatedAt).String() + " ago"

			table.Append([]string{
				c.Name,
				c.ClusterName,
				c.Namespace,
				createdStr,
				updatedStr,
				c.TTL.String(),
				strconv.FormatBool(c.Expired),
			})
		}

		table.Render()
		return nil
	}

	return printutil.PrintWithFormat(sessionsList, c.Output)
}
