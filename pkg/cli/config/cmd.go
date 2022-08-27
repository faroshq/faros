package config

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/faroshq/faros/pkg/models"
)

var Config GlobalConfig

// New returns the cobra command for "config".
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Long:  "Configure CLI operations with new credentials",
		Short: "Configure cli",
		Aliases: []string{
			"configure",
			"configs",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return PersistConfiguration(cmd.Context(), args)
		},
	}

	return cmd
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
	fmt.Println("Configuring CLI.")

	configFile, err := getConfigFile()
	if err != nil {
		return err
	}

	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return err
	}

	var config map[string]string
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return err
	}

	// For persisting right config, we need to resolve Namespace to ID if name was provided
	// For this we init the client, and try resolving the namespace
	if !strings.HasPrefix(Config.Namespace, models.NamespacePrefix) {
		err = InitializeAPIClient()
		if err != nil {
			return err
		}
		namespaces, err := Config.APIClient.ListNamespaces(ctx)
		if err != nil {
			return err
		}
		for _, namespace := range namespaces {
			if strings.EqualFold(namespace.Name, Config.Namespace) {
				Config.Namespace = namespace.ID
				break
			}
		}
	}

	config["api-endpoint"] = Config.APIEndpoint
	config["namespace"] = Config.Namespace
	config["insecureSkipTLSVerify"] = strconv.FormatBool(Config.InsecureSkipTLSVerify)

	data, err = yaml.Marshal(config)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(configFile, data, os.ModePerm)
	if err != nil {
		return err
	}

	fmt.Println("Done")
	return nil
}
