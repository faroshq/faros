package delete

import (
	"github.com/spf13/cobra"

	"github.com/faroshq/faros/pkg/cli/resources/access"
	"github.com/faroshq/faros/pkg/cli/resources/clusters"
	"github.com/faroshq/faros/pkg/cli/resources/namespaces"
)

// New returns new delete wrapper
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete faros resources",
		Long: `
Delete faros resources

Example:
  faros delete --help
  faros delete cluster --help
  faros delete namespace --help
  faros delete access --help
`,
		Aliases: []string{"rm", "remove"},
	}

	cmd.AddCommand(clusters.DeleteCmd())
	cmd.AddCommand(namespaces.DeleteCmd())
	cmd.AddCommand(access.DeleteCmd())

	return cmd
}
