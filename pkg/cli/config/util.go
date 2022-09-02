package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/faroshq/faros/pkg/client"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/httputil"
	"github.com/faroshq/faros/pkg/util/log"
)

var (
	defaultConfigFilename    = "config"
	defaultConfigFileType    = "yaml"
	defaultConfigFileDir     = ".faros"
	defaultKubeConfigFileDir = ".kube"
	defaultKubeConfigFile    = "config"
	envPrefix                = "FAROS"
)

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

	Config.APIClient = client.NewClient(apiEndpointURL, &client.Config{
		Username: Config.Username,
		Password: Config.Password,
	}, httpClient)

	return nil
}

// InitializeConfig will make sure config file exists and is enriched from
// flags with the help of Viper
func InitializeConfig(cmd *cobra.Command) error {
	v := viper.New()

	configFile, err := getConfigFile()
	if err != nil {
		return err
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
Faros.sh CLI configuration not found. Please run:
'faros configure --namespace <namespace_name/namespace_id> --username <username@email.com> --password <password>'
`)
			return nil
		}
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return err
		}
	}

	// When we bind flags to environment variables expect that the
	// environment variables are prefixed, e.g. a flag like --number
	// binds to an environment variable STRING_NUMBER. This helps
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

// EnsureConfigExists will make sure config, used in config file exits in the
// faros. In example if user changed namespace and namespace does not exists.
// Should be executed after config is loaded and we have API client available
func EnsureConfigExists(cmd *cobra.Command) error {
	c := &Config
	ctx := cmd.Context()
	// ensure namespace exists
	namespaces, err := c.APIClient.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	var create bool
	if len(namespaces) == 0 {
		create = true
	}

	for _, namespace := range namespaces {
		if strings.EqualFold(namespace.Name, c.Namespace) {
			_, err := c.APIClient.GetNamespace(ctx, models.Namespace{ID: namespace.ID})
			if err != nil { // TODO: parse error for better api performance
				create = true
			}
		}
	}
	if create {
		_, err := c.APIClient.CreateNamespace(ctx, models.Namespace{
			Name: c.Namespace,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// InitializeLogger will create logger for cmd
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

func TranslateUserConfig(ctx context.Context) error {
	c := &Config

	// nothing to resolve if we dont have a namespace
	if c.APIClient == nil {
		return nil
	}

	var err error
	c.Namespace, err = ResolveNamespaceFlag(ctx, c.Namespace)
	if err != nil {
		return err
	}

	return nil
}

func ResolveNamespaceFlag(ctx context.Context, namespace string) (string, error) {
	c := &Config

	if strings.HasPrefix(namespace, models.NamespacePrefix) {
		return namespace, nil
	} else {
		namespaces, err := c.APIClient.ListNamespaces(ctx)
		if err != nil {
			return "", err
		}
		for _, namespace := range namespaces {
			if strings.EqualFold(namespace.Name, c.Namespace) {
				return namespace.ID, nil
			}
		}
	}
	return "", fmt.Errorf("namespace %s not found", namespace)
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

func ResolveClusterAccessFlag(ctx context.Context, clusterID, session string) (string, error) {
	c := &Config

	if strings.HasPrefix(session, models.ClusterAccessSessionPrefix) {
		return session, nil
	} else {
		sessions, err := c.APIClient.ListClusterAccessSessions(ctx, models.ClusterAccessSession{
			NamespaceID: c.Namespace,
			ClusterID:   clusterID,
		})
		if err != nil {
			return "", err
		}
		for _, c := range sessions {
			if strings.EqualFold(c.Name, session) {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("cluster access session %s not found", session)
}
