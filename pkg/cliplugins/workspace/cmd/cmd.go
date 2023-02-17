package cmd

import (
	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/faroshq/faros/pkg/cliplugins/workspace/plugin"
)

// New provides a cobra command for workload operations.
func New(streams genericclioptions.IOStreams) (*cobra.Command, error) {

	getOptions := plugin.NewGetOptions(streams)

	cmd := &cobra.Command{
		Use:              "workspaces",
		Aliases:          []string{"workspace", "ws"},
		Short:            "Manages workspaces",
		SilenceUsage:     true,
		TraverseChildren: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := getOptions.Complete(args); err != nil {
				return err
			}

			if err := getOptions.Validate(); err != nil {
				return err
			}

			return getOptions.Run(c.Context())
		},
	}

	getCmd := &cobra.Command{
		Use:          "get",
		Short:        "Get an workspaces",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := getOptions.Complete(args); err != nil {
				return err
			}

			if err := getOptions.Validate(); err != nil {
				return err
			}

			return getOptions.Run(c.Context())
		},
	}

	createOptions := plugin.NewCreateOptions(streams)
	createCmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a workspaces",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := createOptions.Complete(args); err != nil {
				return err
			}

			if err := createOptions.Validate(); err != nil {
				return err
			}

			return createOptions.Run(c.Context())
		},
	}

	deleteOptions := plugin.NewDeleteOptions(streams)
	deleteCmd := &cobra.Command{
		Use:          "delete",
		Short:        "Delete a workspaces",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := deleteOptions.Complete(args); err != nil {
				return err
			}

			if err := deleteOptions.Validate(); err != nil {
				return err
			}

			return deleteOptions.Run(c.Context())
		},
	}

	useOptions := plugin.NewUseOptions(streams)
	useCmd := &cobra.Command{
		Use:          "use",
		Short:        "Use a workspaces",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := useOptions.Complete(args); err != nil {
				return err
			}

			if err := useOptions.Validate(); err != nil {
				return err
			}

			return useOptions.Run(c.Context())
		},
	}

	getOptions.BindFlags(getCmd)
	getOptions.BindFlags(cmd)
	cmd.AddCommand(getCmd)

	createOptions.BindFlags(createCmd)
	cmd.AddCommand(createCmd)

	deleteOptions.BindFlags(deleteCmd)
	cmd.AddCommand(deleteCmd)

	useOptions.BindFlags(useCmd)
	cmd.AddCommand(useCmd)

	return cmd, nil
}
