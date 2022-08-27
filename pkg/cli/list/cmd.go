package list

import (
	"github.com/faroshq/faros/pkg/cli/resources/access"
	"github.com/faroshq/faros/pkg/cli/resources/clusters"
	"github.com/faroshq/faros/pkg/cli/resources/namespaces"
	"github.com/spf13/cobra"
)

// New returns new list wrapper
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Long:    "List faros resources",
		Short:   "List faros resources",
		Aliases: []string{"ls"},
	}

	cmd.AddCommand(clusters.ListCmd())
	cmd.AddCommand(namespaces.ListCmd())
	cmd.AddCommand(access.ListCmd())

	return cmd
}
