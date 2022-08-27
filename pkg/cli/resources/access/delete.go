package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/models"
)

func delete(ctx context.Context, args []string, opts opts) error {
	c := &config.Config

	if len(args) == 0 {
		return fmt.Errorf("session name argument missing")
	}

	clusterID, err := config.ResolveClusterFlag(ctx, opts.cluster)
	if err != nil {
		return err
	}

	sessions, err := c.APIClient.ListClusterAccessSessions(ctx, models.ClusterAccessSession{
		NamespaceID: c.Namespace,
		ClusterID:   clusterID,
	})
	if err != nil {
		return errors.ParseCloudError(err)
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions found")
	}

	for _, arg := range args {

		for _, session := range sessions {
			if strings.EqualFold(session.Name, arg) {
				err := c.APIClient.DeleteClusterAccessSession(ctx, session)
				if err != nil {
					return errors.ParseCloudError(err)
				}
				fmt.Printf("Cluster access session %s deleted\n", arg)
			}
		}
	}
	return nil
}
