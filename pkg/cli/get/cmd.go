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
		Use:   "get",
		Short: "Get faros resources",
		Long: `
Get faros resources

Example:
  faros get --help
  faros get cluster --help
  faros get namespace --help
  faros get access --help
`,
		Aliases: []string{"inspect", "show"},
	}

	cmd.AddCommand(clusters.GetCmd())
	cmd.AddCommand(namespaces.GetCmd())
	cmd.AddCommand(access.GetCmd())

	return cmd
}
