package config

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

var Config GlobalConfig

// New returns the cobra command for "config".
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use: "configure",
		Long: `
Configure CLI operations with credentials and other configuration.

Example:
  faros configure --namespace my-namespace --api-endpoint https://api.faros.sh
`,

		Short: "Configure cli session with credentials, namespaces and other configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return PersistConfiguration(cmd.Context(), args)
		},
	}

	return cmd
}

func AppendGlobalFlags(cmd *cobra.Command) error {
	c := &Config

	homedir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cmd.SilenceUsage = true
	cmd.PersistentFlags().StringVarP(&c.LogLevel, "loglevel", "l", "info", "Valid values are [debug, info, warning, error]")
	cmd.PersistentFlags().StringVarP(&c.Output, "output", "o", "table", "Valid values are [table, json, yaml]")
	cmd.PersistentFlags().StringVarP(&c.WorkDir, "work-dir", "w", filepath.Join(homedir, defaultConfigFileDir), "Working directory for CLI")
	cmd.PersistentFlags().StringVar(&c.DefaultKubeConfigLocation, "default-kubeconfig", filepath.Join(homedir, defaultKubeConfigFileDir, defaultKubeConfigFile), "Default kubeconfig file location")
	cmd.PersistentFlags().StringVar(&c.KubeConfigMode, "kubeconfig-mint-mode", "new", "Valid values are [merge, new]")

	cmd.PersistentFlags().StringVar(&c.APIEndpoint, "api-endpoint", "https://localhost:8443/api/v1", "API Endpoint URL")
	cmd.PersistentFlags().StringVarP(&c.Namespace, "namespace", "n", "", "Namespace name or ID")

	cmd.PersistentFlags().BoolVar(&c.InsecureSkipTLSVerify, "insecure-skip-tls-verify", false, "Skip tls verify")

	return nil
}

// New returns the cobra command for "version".
func NewVersion() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Long:  "CLI version",
		Short: "CLI version",
		Run: func(cmd *cobra.Command, args []string) {
			printVersion(cmd)
		},
	}

	return cmd
}

func printVersion(cmd *cobra.Command) {
	fmt.Printf("CLI version: %s\n", "0.0.1")
	os.Exit(0)
}

func PersistConfiguration(ctx context.Context, args []string) error {
	fmt.Println("Configuring CLI...")

	configFile, err := getConfigFile()
	if err != nil {
		return err
	}

	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return err
	}

	var config GlobalConfig
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return err
	}

	config.APIEndpoint = Config.APIEndpoint
	config.Namespace = Config.Namespace
	config.InsecureSkipTLSVerify = Config.InsecureSkipTLSVerify
	config.DefaultKubeConfigLocation = Config.DefaultKubeConfigLocation
	config.Output = Config.Output
	config.LogLevel = Config.LogLevel
	config.KubeConfigMode = Config.KubeConfigMode

	data, err = yaml.Marshal(config)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(configFile, data, os.ModePerm)
	if err != nil {
		return err
	}

	fmt.Printf("Configured CLI. Config file: %s \n", configFile)
	return nil
}
