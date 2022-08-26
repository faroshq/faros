package create

import (
	"github.com/faroshq/faros/pkg/cli/resources/clusters"
	"github.com/faroshq/faros/pkg/cli/resources/namespaces"
	"github.com/spf13/cobra"
)

// New returns new get wrapper
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "create",
		Long:    "Create faros resources",
		Short:   "Create faros resources",
		Aliases: []string{"new"},
	}

	cmd.AddCommand(clusters.CreateCmd())
	cmd.AddCommand(namespaces.CreateCmd())

	return cmd
}
