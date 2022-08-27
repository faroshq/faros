package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/create"
	"github.com/faroshq/faros/pkg/cli/delete"
	"github.com/faroshq/faros/pkg/cli/get"
	"github.com/faroshq/faros/pkg/cli/list"
)

// RunCLI returns user CLI
func RunCLI(ctx context.Context) error {
	cmd := &cobra.Command{
		Short: "Faros CLI",
		Long:  "Faros CLI",
		Use:   "faros --help",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// must run first to populate global config
			err := config.InitializeConfig(cmd)
			if err != nil {
				return err
			}

			err = config.InitializeLogger(cmd)
			if err != nil {
				return err
			}

			err = config.InitializeAPIClient()
			if err != nil {
				return err
			}
			// resolve namespace to namespaceID
			return config.ResolveUserFlags(cmd.Context())
		},
	}

	err := config.EnrichCommonFlags(cmd)
	if err != nil {
		return err
	}

	cmd.AddCommand(
		get.New(),
		list.New(),
		create.New(),
		delete.New(),

		config.New(),
		config.NewVersion(),
	)

	// This will already have global config enriched with values
	return cmd.ExecuteContext(ctx)
}
