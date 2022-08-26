package delete

import (
	"github.com/faroshq/faros/pkg/cli/resources/clusters"
	"github.com/faroshq/faros/pkg/cli/resources/namespaces"
	"github.com/spf13/cobra"
)

// New returns new delete wrapper
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete",
		Long:    "Delete faros resources",
		Short:   "Delete faros resources",
		Aliases: []string{"rm", "remove"},
	}

	cmd.AddCommand(clusters.DeleteCmd())
	cmd.AddCommand(namespaces.DeleteCmd())

	return cmd
}
