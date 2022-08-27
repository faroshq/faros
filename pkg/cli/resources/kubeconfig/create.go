package kubeconfig

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/models"
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

	path := filepath.Join(c.WorkDir, fmt.Sprintf("%s-%s.kubeconfig", opts.cluster, opts.accesssession))
	err = os.WriteFile(path, raw, 0644)
	if err != nil {
		return err
	}

	fmt.Printf("kubeconfig created at %s\n", path)
	fmt.Println("")
	fmt.Printf("export KUBECONFIG=%s\n", path)
	fmt.Println("")

	return nil

}
