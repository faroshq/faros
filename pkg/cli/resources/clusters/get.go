package clusters

import (
	"context"
	"fmt"
	"strings"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	printutil "github.com/faroshq/faros/pkg/cli/util/print"
	"github.com/faroshq/faros/pkg/models"
)

func get(ctx context.Context, args []string) error {
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

	for _, cluster := range clusters {
		if strings.EqualFold(cluster.Name, args[0]) {
			return printutil.PrintWithFormat(cluster, printutil.OverrideTable(c.Output))
		}
	}
	return fmt.Errorf("cluster %s not found", args[0])

}
