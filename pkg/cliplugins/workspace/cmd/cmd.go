package cmd

import (
	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/faroshq/faros/pkg/cliplugins/workspace/plugin"
)

// New provides a cobra command for workload operations.
func New(streams genericclioptions.IOStreams) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:              "workspaces",
		Short:            "Manages workspaces",
		SilenceUsage:     true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	getOptions := plugin.NewGetOptions(streams)
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

	getOptions.BindFlags(getCmd)
	cmd.AddCommand(getCmd)

	createOptions.BindFlags(createCmd)
	cmd.AddCommand(createCmd)

	deleteOptions.BindFlags(deleteCmd)
	cmd.AddCommand(deleteCmd)

	return cmd, nil
}
