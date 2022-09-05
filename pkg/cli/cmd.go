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
		Long: `
Faros CLI is a command line interface for raros.sh`,
		Use: "faros --help",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			fMap := []func(*cobra.Command) error{
				config.InitializeConfig, // must be first
				config.InitializeLogger, // must be after config
				config.InitializeAPIClient,
				config.EnsureObjectExists,  // must be before client
				config.TranslateUserConfig, // must ve after translate
			}
			for _, f := range fMap {
				err := f(cmd)
				if err != nil {
					return err
				}
			}

			return nil
		},
	}

	err := config.AppendGlobalFlags(cmd)
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
