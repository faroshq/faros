package create

import (
	"github.com/spf13/cobra"

	"github.com/faroshq/faros/pkg/cli/resources/access"
	"github.com/faroshq/faros/pkg/cli/resources/clusters"
	"github.com/faroshq/faros/pkg/cli/resources/kubeconfig"
	"github.com/faroshq/faros/pkg/cli/resources/namespaces"
)

// New returns new get wrapper
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create faros resources",
		Long: `
Create faros resources

Example:
  faros create --help
  faros create cluster --help
  faros create namespace --help
  faros create kubeconfig --help
  faros create access --help
`,
		Aliases: []string{"new"},
	}

	cmd.AddCommand(clusters.CreateCmd())
	cmd.AddCommand(namespaces.CreateCmd())
	cmd.AddCommand(access.CreateCmd())
	cmd.AddCommand(kubeconfig.CreateCmd())

	return cmd
}
