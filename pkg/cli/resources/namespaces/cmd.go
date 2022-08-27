package namespaces

import (
	"github.com/spf13/cobra"
)

func GetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "namespaces",
		Short: "Get namespaces",
		Long: `
Get namespace in Faros.

Example:
  faros get namespace namespace_name

`,
		Aliases: []string{
			"namespace",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return get(cmd.Context(), args)
		},
	}
}

func ListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "namespaces",
		Short: "List namespaces",
		Long: `
List namespaces in Faros.

Example:
  faros list namespaces

`,
		Aliases: []string{
			"namespace",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return list(cmd.Context())
		},
	}
}

func DeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "namespaces",
		Short: "Delete namespaces",
		Long: `
Delete namespaces in Faros. Once namespace is deleted it will delete all clusters,
access sessions and other resources associated with the namespace.

Example:
  faros delete namespaces namespace_name namespace_name2

`,
		Aliases: []string{
			"namespace",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return delete(cmd.Context(), args)
		},
	}
}

func CreateCmd() *cobra.Command {
	cc := &cobra.Command{
		Use:   "namespace",
		Short: "Create a namespace",
		Long: `Create a namespace
Usage:
	faros namespace create namespace_name

`,
		Aliases: []string{
			"namespace",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return create(cmd.Context(), args)
		},
	}

	return cc
}
