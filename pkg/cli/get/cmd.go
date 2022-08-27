package get

import (
	"github.com/spf13/cobra"

	"github.com/faroshq/faros/pkg/cli/resources/access"
	"github.com/faroshq/faros/pkg/cli/resources/clusters"
	"github.com/faroshq/faros/pkg/cli/resources/namespaces"
)

// New returns new get wrapper
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get",
		Long:    "Get faros resources",
		Short:   "Get faros resources",
		Aliases: []string{"inspect", "show"},
	}

	cmd.AddCommand(clusters.GetCmd())
	cmd.AddCommand(namespaces.GetCmd())
	cmd.AddCommand(access.GetCmd())

	return cmd
}
