package clusters

import (
	"github.com/spf13/cobra"
)

func GetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Long:  "Get cluster",
		Short: "Get cluster",
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
		Long:  "List cluster",
		Short: "List cluster",
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
		Long:  "Delete clusters",
		Short: "Delete cluster",
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
		Long: `Create a cluster
Usage:
	faros clusters create cluster_name -f <kubeconfig_location>
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
