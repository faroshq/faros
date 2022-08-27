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
		Long:  "Get cluster access sessions",
		Short: "Get cluster access sessions",
		Aliases: []string{
			"clusteraccess", "clusteraccesssession", "session",
		},
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
		Long:  "Get cluster access sessions",
		Short: "Get cluster access sessions",
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
		Short: "Get cluster access sessions",

		Long: `Get cluster access sessions
Usage:
	faros create access access_name -c <cluster_name> -t <ttl>
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
