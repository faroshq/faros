package azure

import (
	"context"
	"encoding/base64"
	"sync"
	"time"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"

	"github.com/faroshq/faros/pkg/cloud"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/azureclients/mgmt/containerservice"
	"github.com/faroshq/faros/pkg/util/recover"
	"github.com/sirupsen/logrus"
)

var _ cloud.Cloud = (*azureProvider)(nil)

// clients is list of clients based on subscriptions IDs
type clients struct {
	subscriptionID   string
	authorizer       autorest.Authorizer
	containerservice containerservice.ManagedCluster
}

type azureProvider struct {
	log    *logrus.Entry
	config *config.Config

	clientList []clients

	clusterListCache []models.Cluster
	clusterListLock  sync.RWMutex
}

func New(ctx context.Context, log *logrus.Entry, config *config.Config) (cloud.Cloud, error) {
	authorizersList, err := newAuthorizers(ctx, log, config)
	if err != nil {
		return nil, err
	}
	clientsList := []clients{}
	for subscriptionID, authorizer := range authorizersList {
		clientsList = append(clientsList, clients{
			subscriptionID:   subscriptionID,
			authorizer:       authorizer,
			containerservice: containerservice.NewManagedClusterClient(subscriptionID, authorizer),
		})
	}
	return &azureProvider{
		log:        log,
		config:     config,
		clientList: clientsList,
	}, nil
}

// Run will run clusters and update cluster cache with all clusters in the cloud
func (a *azureProvider) Run(ctx context.Context) error {
	defer recover.Panic(a.log)

	ticker := time.NewTicker(a.config.Controller.SessionExpireInterval)
	defer ticker.Stop()

	for {

		clusters, err := a.listClusters(ctx)
		if err != nil {
			return err
		}
		a.clusterListLock.Lock()
		a.clusterListCache = clusters
		a.clusterListLock.Unlock()

		select {
		case <-ctx.Done():
			a.log.Info("stopped azure provider service")
			return nil
		case <-ticker.C:
		}
	}
}

func (a *azureProvider) ListClusters(ctx context.Context) ([]models.Cluster, error) {
	a.clusterListLock.RLock()
	defer a.clusterListLock.RUnlock()
	return a.clusterListCache, nil
}

func (a *azureProvider) listClusters(ctx context.Context) ([]models.Cluster, error) {
	var result []models.Cluster
	for _, client := range a.clientList {
		clusters, err := client.containerservice.ListAll(ctx)
		if err != nil {
			a.log.WithError(err).Warnf("failed to list azure clusters for subscriptionID: %s", client.subscriptionID)
			continue
		}
		for _, cluster := range clusters {
			resource, err := azure.ParseResourceID(*cluster.ID)
			if err != nil {
				a.log.WithError(err).Warnf("failed to parse azure cluster id: %s", *cluster.ID)
				continue
			}
			credentials, err := client.containerservice.ListClusterAdminCredentials(ctx, resource.ResourceGroup, resource.ResourceName, *cluster.Fqdn)
			if err != nil {
				a.log.WithError(err).Warnf("failed to list azure cluster credentials for subscriptionID: %s and cluster: %s", client.subscriptionID, *cluster.ID)
				continue
			}
			if credentials.Kubeconfigs == nil {
				a.log.Warnf("failed to get kubeconfigs for subscriptionID: %s and cluster: %s", client.subscriptionID, *cluster.ID)
				continue
			}
			if credentials.Kubeconfigs == nil ||
				len(*credentials.Kubeconfigs) == 0 ||
				*(*credentials.Kubeconfigs)[0].Value == nil {
				a.log.Warnf("failed to get kubeconfigs for subscriptionID: %s and cluster: %s", client.subscriptionID, *cluster.ID)
				continue
			}

			kubeConfig := *(*credentials.Kubeconfigs)[0].Value

			result = append(result, models.Cluster{
				Name: *cluster.ID,
				Config: models.ClusterConfig{
					RawKubeConfig: base64.RawStdEncoding.EncodeToString(kubeConfig),
				},
			})
		}
	}
	return result, nil
}
