package clusters

import (
	"context"
	"fmt"
	"strings"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/models"
)

func delete(ctx context.Context, args []string) error {
	c := &config.Config

	if len(args) == 0 {
		return fmt.Errorf("cluster name argument missing")
	}

	clusters, err := c.APIClient.ListClusters(ctx, models.Cluster{
		NamespaceID: c.Namespace,
	})
	if err != nil {
		return errors.ParseCloudError(err)
	}
	if len(clusters) == 0 {
		return fmt.Errorf("no clusters found")
	}

	for _, arg := range args {

		for _, cluster := range clusters {
			if strings.EqualFold(cluster.Name, arg) {
				err := c.APIClient.DeleteCluster(ctx, cluster)
				if err != nil {
					return errors.ParseCloudError(err)
				}
				fmt.Printf("Cluster %s deleted\n", arg)
			}
		}
	}
	return nil
}
