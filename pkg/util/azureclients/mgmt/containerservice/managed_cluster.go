package containerservice

import (
	"context"

	mgmtcontainerservice "github.com/Azure/azure-sdk-for-go/services/containerservice/mgmt/2022-03-01/containerservice"
	"github.com/Azure/go-autorest/autorest"
)

// ManagedCluster is a minimal interface for azure managed clusters
type ManagedCluster interface {
	ListAll(ctx context.Context) (result []mgmtcontainerservice.ManagedCluster, err error)
	ListClusterAdminCredentials(ctx context.Context, resourceGroupName string, resourceName string, serverFqdn string) (result mgmtcontainerservice.CredentialResults, err error)
}

type managedCluster struct {
	mgmtcontainerservice.ManagedClustersClient
}

var _ ManagedCluster = &managedCluster{}

// NewManagedClusterClient creates a new ManagedCluster
func NewManagedClusterClient(subscriptionID string, authorizer autorest.Authorizer) ManagedCluster {
	client := mgmtcontainerservice.NewManagedClustersClient(subscriptionID)
	client.Authorizer = authorizer

	return &managedCluster{
		ManagedClustersClient: client,
	}
}

// ListAll list all managed clusters without pagination
func (c managedCluster) ListAll(ctx context.Context) (result []mgmtcontainerservice.ManagedCluster, err error) {
	page, err := c.List(ctx)
	if err != nil {
		return nil, err
	}

	for page.NotDone() {
		result = append(result, page.Values()...)
		err = page.Next()
		if err != nil {
			return nil, err
		}
	}

	return result, nil

}
