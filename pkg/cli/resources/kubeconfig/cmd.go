package kubeconfig

import (
	"github.com/spf13/cobra"
)

type opts struct {
	cluster       string
	accesssession string
}

func CreateCmd() *cobra.Command {
	var opts opts
	cc := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Generate cluster kubeconfig",
		Long: `
Generate cluster kubeconfig for cluster access session. Session must exists
before generating kubeconfig. Once kubeconfig is generated, its token is not
retrievable.

Example:
  faros create kubeconfig -c <cluster_name> -s <access_session_name>
  faros create kubeconfig -c <cluster_name> -s <access_session_name> -n <namespace_name>
`,
		Aliases: []string{
			"kc", "kube-config",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return create(cmd.Context(), args, opts)
		},
	}

	cc.Flags().StringVarP(&opts.cluster, "cluster", "c", "", "Cluster name or ID")
	cc.Flags().StringVarP(&opts.accesssession, "access-session", "s", "", "Cluster access session name or ID")

	return cc
}
