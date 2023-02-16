package bootstrap

import (
	"context"
	"embed"
	"os"
	"path/filepath"

	kcpclient "github.com/kcp-dev/kcp/pkg/client/clientset/versioned/cluster"

	"github.com/faroshq/faros/pkg/config"
	utilkubernetes "github.com/faroshq/faros/pkg/util/kubernetes"
)

//go:generate cp -r ../../config ./
//go:embed config/*/*.yaml
var fs embed.FS

type Bootstraper interface {
	CreateWorkspace(ctx context.Context, name string) error
	BootstrapServiceTenantAssets(ctx context.Context) error
	DeployKustomizeAssetsCRD(ctx context.Context) error
	DeployKustomizeAssetsKCP(ctx context.Context) error
}

type bootstrap struct {
	config *config.FarosKCPConfig

	clientFactory utilkubernetes.ClientFactory
	kcpClient     kcpclient.ClusterInterface
}

func New(config *config.FarosKCPConfig) (*bootstrap, error) {
	cf, err := utilkubernetes.NewClientFactory(config.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	rootRest, err := cf.GetRootRestConfig()
	if err != nil {
		return nil, err
	}

	client, err := kcpclient.NewForConfig(rootRest)
	if err != nil {
		return nil, err
	}

	b := &bootstrap{
		config:        config,
		clientFactory: cf,
		kcpClient:     client,
	}

	return b, nil
}

func (b *bootstrap) DeployKustomizeAssetsCRD(ctx context.Context) error {
	workspace := b.config.ControllersWorkspace
	return b.deployKustomizeComponents(ctx, workspace, "config/crds")
}

func (b *bootstrap) DeployKustomizeAssetsKCP(ctx context.Context) error {
	workspace := b.config.ControllersWorkspace
	return b.deployKustomizeComponents(ctx, workspace, "config/kcp")
}

func (b *bootstrap) deployKustomizeComponents(ctx context.Context, workspace, baseDir string) error {
	tmpDir, err := os.MkdirTemp("", "faros-kcp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	f, err := fs.ReadDir(baseDir)
	if err != nil {
		return err
	}
	for _, file := range f {
		if file.IsDir() {
			continue
		}
		path := filepath.Join(baseDir, file.Name())
		data, err := fs.ReadFile(path)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return err
		}

		if err := os.WriteFile(filepath.Join(tmpDir, file.Name()), data, 0644); err != nil {
			return err
		}
	}

	return b.deployComponents(ctx, workspace, tmpDir)
}

// CreateWorkspace creates a new workspace with the given name recursively.
func (b *bootstrap) CreateWorkspace(ctx context.Context, name string) error {
	return b.createNamedWorkspace(ctx, name)
}

func (b *bootstrap) BootstrapServiceTenantAssets(ctx context.Context) error {
	source := b.config.ControllersWorkspace
	target := b.config.ControllersTenantWorkspace
	err := b.bootstrapServiceTenantAssets(ctx, source, target)
	if err != nil {
		return err
	}
	return b.bootstrapRootTenantAssets(ctx)
}
