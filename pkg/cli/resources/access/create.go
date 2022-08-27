package access

import (
	"context"
	"fmt"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/models"
)

func create(ctx context.Context, args []string, opts opts) error {
	c := &config.Config

	if len(args) == 0 { // no file and not args...
		return fmt.Errorf("name argument missing")
	}
	name := args[0]

	if opts.cluster == "" {
		return fmt.Errorf("cluster name flag missing")
	}

	clusterID, err := config.ResolveClusterFlag(ctx, opts.cluster)
	if err != nil {
		return err
	}

	session := models.ClusterAccessSession{
		Name:        name,
		NamespaceID: c.Namespace,
		ClusterID:   clusterID,
		TTL:         opts.ttl,
	}

	sessions, err := c.APIClient.ListClusterAccessSessions(ctx, session)
	if err != nil {
		return errors.ParseCloudError(err)
	}
	for _, c := range sessions {
		if c.Name == name {
			return fmt.Errorf("cluster access session %s already exists", name)
		}
	}

	result, err := c.APIClient.CreateClusterAccessSession(ctx, session)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	fmt.Printf("Cluster access session %s successfully created at %s!\n", result.Name, result.CreatedAt.Local().Format("Mon Jan _2 15:04:05 2006"))
	return nil

}
