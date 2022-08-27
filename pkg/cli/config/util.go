package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/faroshq/faros/pkg/client"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/file"
	httputil "github.com/faroshq/faros/pkg/util/http"
	"github.com/faroshq/faros/pkg/util/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var defaultConfigFilename = "config"
var defaultConfigFileType = "yaml"
var defaultConfigFileDir = ".faros"
var envPrefix = "FAROS"

func InitializeAPIClient() error {
	apiEndpointURL, err := url.Parse(Config.APIEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse API endpoint URL '%s', error: %w", Config.APIEndpoint, err)
	}

	httpClient := httputil.DefaultClient
	if Config.InsecureSkipTLSVerify {
		fmt.Println("WARNING: InsecureSkipTLSVerify is enabled")
		httpClient = httputil.DefaultInsecureClient
	}

	Config.APIClient = client.NewClient(apiEndpointURL, httpClient)

	return nil
}

func InitializeConfig(cmd *cobra.Command) error {
	v := viper.New()

	configFile, err := getConfigFile()
	if err != nil {
		return err
	}

	// TODO: remove this once we move from config to config.yaml
	oldConfig := strings.Trim(configFile, "."+defaultConfigFileType)
	if exist, _ := file.Exist(oldConfig); exist {
		fmt.Printf("Migrating CLI config file\n")
		err := file.MoveFile(oldConfig, configFile)
		if err != nil {
			fmt.Printf("failed to migrate CLI configuration format: %s", err)
		}
	}

	v.SetConfigFile(configFile)

	err = os.MkdirAll(filepath.Dir(configFile), os.ModePerm)
	if err != nil {
		return err
	}

	// If configure action is called - we skip early as it is re-configure step
	// Attempt to read the config file, gracefully ignoring errors
	// caused by a config file not being found. Return an error
	// if we cannot parse the config file.
	err = v.ReadInConfig()
	if err != nil && cmd.CalledAs() != "configure" {
		// It's okay if there isn't a config file
		if os.IsNotExist(err) {
			fmt.Printf(`
Faros cluster registry CLI configuration not found. Please run:
'faros configure --namespace <namespace_name/namespace_id>'
`)
			return nil
		}
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return err
		}
	}

	// When we bind flags to environment variables expect that the
	// environment variables are prefixed, e.g. a flag like --number
	// binds to an environment variable STING_NUMBER. This helps
	// avoid conflicts.
	v.SetEnvPrefix(envPrefix)

	// Bind to environment variables
	v.AutomaticEnv()

	// Bind the current command's flags to viper
	bindFlags(cmd, v)

	err = v.WriteConfigAs(configFile)
	if err != nil {
		return err
	}

	return nil
}

func InitializeLogger(cmd *cobra.Command) error {
	Config.Log = log.GetLogger()
	return nil
}

// Bind each cobra flag to its associated viper configuration (config file and environment variable)
func bindFlags(cmd *cobra.Command, v *viper.Viper) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		// Environment variables can't have dashes in them, so bind them to their equivalent
		// keys with underscores, e.g. --favorite-color to STING_FAVORITE_COLOR
		envVarSuffix := f.Name
		if strings.Contains(f.Name, "-") {
			envVarSuffix = strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		}
		v.BindEnv(f.Name, fmt.Sprintf("%s_%s", envPrefix, envVarSuffix))

		// Apply the viper config value to the flag when the flag is not set and viper has a value
		if !f.Changed && v.IsSet(f.Name) {
			val := v.Get(f.Name)
			cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
		}
	})
}

func EnrichCommonFlags(cmd *cobra.Command) error {
	c := &Config

	homedir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cmd.SilenceUsage = true
	cmd.PersistentFlags().StringVarP(&c.LogLevel, "loglevel", "l", "info", "Valid values are [debug, info, warning, error]")
	cmd.PersistentFlags().StringVarP(&c.Output, "output", "o", "table", "Valid values are [table, json, yaml]")
	cmd.PersistentFlags().StringVarP(&c.WorkDir, "work-dir", "w", filepath.Join(homedir, defaultConfigFileDir), "Working directory for CLI")

	cmd.PersistentFlags().StringVar(&c.APIEndpoint, "controller-uri", "https://localhost:8443/api/v1", "API Endpoint URL")
	cmd.PersistentFlags().MarkHidden("controller-uri")

	cmd.PersistentFlags().StringVarP(&c.Namespace, "namespace", "n", "", "Namespace name or ID")
	cmd.PersistentFlags().MarkHidden("namespace")

	cmd.PersistentFlags().BoolVar(&c.InsecureSkipTLSVerify, "insecureSkipTLSVerify", false, "skip tls verify")
	cmd.PersistentFlags().MarkHidden("insecureSkipTLSVerify")

	return nil
}

func getConfigFile() (string, error) {
	configDir, err := getConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(configDir, defaultConfigFilename+"."+defaultConfigFileType), nil
}

func getConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, defaultConfigFileDir), nil
}

func ResolveUserFlags(ctx context.Context) error {
	c := &Config

	if strings.HasPrefix(c.Namespace, models.NamespacePrefix) {
		return nil
	} else {
		namespaces, err := c.APIClient.ListNamespaces(ctx)
		if err != nil {
			return err
		}
		for _, namespace := range namespaces {
			if strings.EqualFold(namespace.Name, c.Namespace) {
				c.Namespace = namespace.ID
			}
		}
	}

	return nil
}

func ResolveClusterFlag(ctx context.Context, cluster string) (string, error) {
	c := &Config

	if strings.HasPrefix(cluster, models.ClusterPrefix) {
		return cluster, nil
	} else {
		clusters, err := c.APIClient.ListClusters(ctx, models.Cluster{
			NamespaceID: c.Namespace,
		})
		if err != nil {
			return "", err
		}
		for _, c := range clusters {
			if strings.EqualFold(c.Name, cluster) {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("cluster %s not found", cluster)
}
