package list

import (
	"github.com/spf13/cobra"

	"github.com/faroshq/faros/pkg/cli/resources/access"
	"github.com/faroshq/faros/pkg/cli/resources/clusters"
	"github.com/faroshq/faros/pkg/cli/resources/namespaces"
)

// New returns new list wrapper
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List faros resources",
		Long: `
List faros resources

Example:
  faros list --help
  faros list cluster --help
  faros list namespace --help
  faros list access --help
`,
		Aliases: []string{"ls"},
	}

	cmd.AddCommand(clusters.ListCmd())
	cmd.AddCommand(namespaces.ListCmd())
	cmd.AddCommand(access.ListCmd())

	return cmd
}
