package subscription

import (
	"context"

	mgmtsubscription "github.com/Azure/azure-sdk-for-go/services/subscription/mgmt/2020-09-01/subscription"
	"github.com/Azure/go-autorest/autorest"
)

// SubscriptionClient is a minimal interface for azure Subscription client
type SubscriptionClient interface {
	ListAll(ctx context.Context) (result []mgmtsubscription.Model, err error)
}

type subscriptionsClient struct {
	mgmtsubscription.SubscriptionsClient
}

var _ SubscriptionClient = &subscriptionsClient{}

// NewSubscriptionClient creates a new SubscriptionClient
func NewSubscriptionClient(authorizer autorest.Authorizer) SubscriptionClient {
	client := mgmtsubscription.NewSubscriptionsClient()
	client.Authorizer = authorizer

	return &subscriptionsClient{
		SubscriptionsClient: client,
	}
}

// ListAll will list all subscriptions without pagination
func (c *subscriptionsClient) ListAll(ctx context.Context) (result []mgmtsubscription.Model, err error) {
	page, err := c.SubscriptionsClient.ListComplete(ctx)
	if err != nil {
		return nil, err
	}

	for page.NotDone() {

		result = append(result, page.Value())
		err = page.Next()
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}
