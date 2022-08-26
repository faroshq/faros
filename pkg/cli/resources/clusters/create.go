package clusters

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/cli/util/validation"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/file"
)

func create(ctx context.Context, opts createOps, args []string, update bool) error {
	c := &config.Config

	if len(args) == 0 { // no file and not args...
		return fmt.Errorf("cluster name argument missing")
	}
	clusterName := args[0]

	if opts.kubeConfigLocation == "" {
		return fmt.Errorf("kubeconfig location argument missing")
	}

	cluster := models.Cluster{
		Name:        clusterName,
		NamespaceID: c.Namespace,
	}

	var data []byte
	if opts.kubeConfigLocation != "" { // create from file/url
		if validation.IsValidUrl(opts.kubeConfigLocation) {
			resp, err := http.Get(opts.kubeConfigLocation)
			if err != nil {
				return fmt.Errorf("url [%s] not found", opts.kubeConfigLocation)
			}
			data, err = ioutil.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("url [%s] failed to read", opts.kubeConfigLocation)
			}
		} else {
			exists, _ := file.Exist(opts.kubeConfigLocation)
			if !exists {
				return fmt.Errorf("file [%s] not found", opts.kubeConfigLocation)
			}
			var err error
			data, err = ioutil.ReadFile(opts.kubeConfigLocation)
			if err != nil {
				return err
			}
		}
		// encode to base64
		cluster.Config.RawKubeConfig = base64.RawStdEncoding.EncodeToString(data)
	} else {
		// TODO: create from args
	}

	clusters, err := c.APIClient.ListClusters(ctx, cluster.NamespaceID)
	if err != nil {
		return errors.ParseCloudError(err)
	}
	for _, c := range clusters {
		if c.Name == clusterName {
			return fmt.Errorf("cluster %s already exists", clusterName)
		}
	}

	result, err := c.APIClient.CreateCluster(ctx, cluster)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	fmt.Printf("Cluster %s successfully created at %s!\n", result.Name, result.CreatedAt.Local().Format("Mon Jan _2 15:04:05 2006"))
	return nil

}
