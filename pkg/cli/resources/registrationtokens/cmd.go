package registrationtokens

import (
	"github.com/spf13/cobra"
)

type opts struct {
	cluster string
}

func CreateCmd() *cobra.Command {
	var opts opts
	cc := &cobra.Command{
		Use:   "registration-token",
		Short: "Generate cluster registration token & config",
		Long: `
Generate cluster agent registration token and config. Once agent calls back it
will register itself as cluster and create access session for the cluster. Access
session will be used to authenticate cluster agent after handshake. Once handshake is complete
secret with access key will be updated. Registration token will be disabled and can't be used again.

Example:
  faros create registration-token -c <cluster_name>
  faros create registration-token -c <cluster_name> -n <namespace_name>
`,
		Aliases: []string{
			"agent-config", "agent-token", "agent-registration-token",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return create(cmd.Context(), args, opts)
		},
	}

	cc.Flags().StringVarP(&opts.cluster, "cluster", "c", "", "Cluster name")

	return cc
}
