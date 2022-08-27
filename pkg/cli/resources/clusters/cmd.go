package clusters

import (
	"github.com/spf13/cobra"
)

func GetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "Get cluster",
		Long: `
Get individual cluster by name or ID

Example:
  faros get clusters cluster_name
`,
		Aliases: []string{
			"clusters",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return get(cmd.Context(), args)
		},
	}
}

func ListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "List cluster",
		Long: `
List cluster by namespace

Example:
  faros list clusters
  faros list clusters -n <namespace_name>

`,
		Aliases: []string{
			"cluster",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return list(cmd.Context())
		},
	}
}

func DeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "Delete cluster",
		Long: `
Delete cluster by name or ID. Cluster deletion will revoke all sessions for the cluster.

Example:
  faros delete clusters cluster_name cluster_name2

  `,
		Aliases: []string{
			"cluster",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return delete(cmd.Context(), args)
		},
	}
}

type createOps struct {
	kubeConfigLocation string
}

func CreateCmd() *cobra.Command {
	var opts createOps
	cc := &cobra.Command{
		Use:   "clusters",
		Short: "Create a cluster",
		Long: `
Create a cluster for Faros to manage.

Usage:
	faros create cluster cluster_name -f <kubeconfig_location>
	faros create cluster cluster_name -f <kubeconfig_location> -n <namespace>

`,
		Aliases: []string{
			"cluster",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return create(cmd.Context(), opts, args, false)
		},
	}

	cc.Flags().StringVarP(&opts.kubeConfigLocation, "kubeconfig", "k", "", "Kubeconfig location")
	return cc
}
