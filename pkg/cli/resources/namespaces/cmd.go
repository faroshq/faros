package namespaces

import (
	"github.com/spf13/cobra"
)

func GetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "namespaces",
		Long:  "Get namespaces",
		Short: "Get namespaces",
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
		Long:  "List namespaces",
		Short: "List namespaces",
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
		Long:  "Delete namespaces",
		Short: "Delete namespaces",
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
