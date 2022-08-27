package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	printutil "github.com/faroshq/faros/pkg/cli/util/print"
	"github.com/faroshq/faros/pkg/models"
)

func get(ctx context.Context, args []string, opts opts) error {
	c := &config.Config

	if opts.cluster == "" {
		return fmt.Errorf("cluster name flag missing")
	}

	var err error
	opts.cluster, err = config.ResolveClusterFlag(ctx, opts.cluster)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return fmt.Errorf("session name argument missing")
	}

	sessions, err := c.APIClient.ListClusterAccessSessions(ctx, models.ClusterAccessSession{
		NamespaceID: c.Namespace,
		ClusterID:   opts.cluster,
	})
	if err != nil {
		return errors.ParseCloudError(err)
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions found")
	}

	for _, session := range sessions {
		if strings.EqualFold(session.Name, args[0]) {
			return printutil.PrintWithFormat(session, printutil.OverrideTable(c.Output))
		}
	}
	return fmt.Errorf("session %s not found", args[0])

}
