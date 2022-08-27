package access

import (
	"time"

	"github.com/spf13/cobra"
)

type opts struct {
	cluster string
	ttl     time.Duration
}

func GetCmd() *cobra.Command {
	var opts opts
	cc := &cobra.Command{
		Use:   "access",
		Long:  "Get cluster access sessions",
		Short: "Get cluster access sessions",
		Aliases: []string{
			"clusteraccess", "clusteraccesssession", "session",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return get(cmd.Context(), args, opts)
		},
	}

	cc.Flags().StringVarP(&opts.cluster, "cluster", "c", "", "Cluster name or ID")

	return cc
}

func ListCmd() *cobra.Command {
	var opts opts
	cc := &cobra.Command{
		Use:   "access",
		Short: "List cluster access sessions",
		Aliases: []string{
			"clusteraccess", "clusteraccesssession", "session",
		},
		Long: `
Get cluster access sessions. You can have multiple sessions for a cluster.
Session has TTL assigned to them. Once the TTL expires, the session is invalid and
will be deleted after some time.

Example:
	faros list access -c <cluster_name>
	faros list access -c <cluster_name> -n <namespace_name>

`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return list(cmd.Context(), opts)
		},
	}
	cc.Flags().StringVarP(&opts.cluster, "cluster", "c", "", "Cluster name or ID")
	return cc
}

func DeleteCmd() *cobra.Command {
	var opts opts
	cc := &cobra.Command{
		Use:   "access",
		Short: "Delete cluster access sessions",
		Long: `
Delete cluster access sessions. You can have multiple sessions for a cluster.
Once session is deleted, all kubeconfigs for the session are also deleted.

Example:
	faros delete access -c <cluster_name>
	faros delete access -c <cluster_name> -n <namespace_name>

`,
		Aliases: []string{
			"clusteraccess", "clusteraccesssession", "session",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return delete(cmd.Context(), args, opts)
		},
	}

	cc.Flags().StringVarP(&opts.cluster, "cluster", "c", "", "Cluster name or ID")
	return cc
}

func CreateCmd() *cobra.Command {
	var opts opts
	cc := &cobra.Command{
		Use:   "access",
		Short: "Create cluster access sessions",
		Long: `
Create cluster access sessions. Session creation enables you to access
the cluster without having to log in to the cluster.

Once session is created you can generate kubeconfig for the session.

Usage:
	faros create access access_name -c <cluster_name> -t <ttl>
	faros create kubeconfig -s access_name -c <cluster_name>

`,
		Aliases: []string{
			"clusteraccess", "clusteraccesssession", "session",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return create(cmd.Context(), args, opts)
		},
	}

	cc.Flags().StringVarP(&opts.cluster, "cluster", "c", "", "Cluster name or ID")
	cc.Flags().DurationVarP(&opts.ttl, "ttl", "t", time.Hour*24, "Duration to keep access session alive")

	return cc
}
