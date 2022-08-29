package kubeconfig

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/file"
)

func create(ctx context.Context, args []string, opts opts) error {
	c := &config.Config

	if opts.cluster == "" {
		return fmt.Errorf("cluster name flag missing")
	}

	clusterID, err := config.ResolveClusterFlag(ctx, opts.cluster)
	if err != nil {
		return err
	}

	if opts.accesssession == "" {
		return fmt.Errorf("access-session flag missing")
	}

	sessionID, err := config.ResolveClusterAccessFlag(ctx, clusterID, opts.accesssession)
	if err != nil {
		return err
	}

	session := models.ClusterAccessSession{
		NamespaceID: c.Namespace,
		ClusterID:   clusterID,
		ID:          sessionID,
	}

	result, err := c.APIClient.CreateClusterAccessSessionKubeConfig(ctx, session)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	raw, err := base64.RawStdEncoding.DecodeString(result.KubeConfig)
	if err != nil {
		return err
	}

	clusterKubeconfigName := fmt.Sprintf("%s-%s", opts.cluster, opts.accesssession)
	path := filepath.Join(c.WorkDir, fmt.Sprintf("%s.kubeconfig", clusterKubeconfigName))
	if strings.EqualFold(c.KubeConfigMode, "new") {
		err = os.WriteFile(path, raw, 0644)
		if err != nil {
			return err
		}
	} else {
		if exists, _ := file.Exist(c.DefaultKubeConfigLocation); !exists {
			fmt.Printf("default kubeconfig %s does not exist. Creating\n", c.DefaultKubeConfigLocation)
			err = os.WriteFile(c.DefaultKubeConfigLocation, raw, 0644)
			if err != nil {
				return err
			}
			fmt.Printf("kubeconfig created at %s\n", path)
			fmt.Println("")
			fmt.Printf("export KUBECONFIG=%s\n", path)
			fmt.Println("")
			return nil
		} else {
			current, err := clientcmd.LoadFromFile(c.DefaultKubeConfigLocation)
			if err != nil {
				return err
			}

			new, err := clientcmd.NewClientConfigFromBytes(raw)
			if err != nil {
				return err
			}

			merged, err := mergeKubeConfig(clusterKubeconfigName, *current, new)
			if err != nil {
				return err

			}

			err = clientcmd.WriteToFile(*merged, c.DefaultKubeConfigLocation)
			if err != nil {
				return err
			}
			fmt.Printf("kubeconfig merged at %s\n", c.DefaultKubeConfigLocation)
			fmt.Println("")
			fmt.Printf("export KUBECONFIG=%s\n", c.DefaultKubeConfigLocation)
			fmt.Printf("set context:\n")
			fmt.Printf("kubectl config use-context %s\n", clusterKubeconfigName)
			fmt.Println("")
		}
	}
	return nil
}

func mergeKubeConfig(name string, current clientcmdapi.Config, new clientcmd.ClientConfig) (*clientcmdapi.Config, error) {
	newC, err := new.RawConfig()
	if err != nil {
		return nil, err
	}

	current.CurrentContext = name
	current.Clusters[name] = newC.Clusters[newC.CurrentContext]
	current.AuthInfos[name] = newC.AuthInfos["user"]
	context := newC.Contexts[newC.CurrentContext]
	context.AuthInfo = name
	context.Cluster = name
	current.Contexts[name] = context
	return &current, nil
}
