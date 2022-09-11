package azure

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure/auth"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/util/azureclients/mgmt/subscription"
	"github.com/sirupsen/logrus"
)

// authorizersList is list of authorizers based on subscriptions IDs
type authorizersList map[string]autorest.Authorizer

// newAuthorizers will return list of authorizers based on subscriptions IDs.
// If no subscription ID is configured, it will attempt to list all subscriptions
// Currently we don't support cross tenant access
func newAuthorizers(ctx context.Context, log *logrus.Entry, c *config.Config) (authorizersList, error) {
	if c.Controller.AzureCredentials.ClientID == "" {
		return nil, fmt.Errorf("no azure client id configured")
	}
	list := make(authorizersList)
	if c.Controller.AzureCredentials.SubscriptionID == "" {
		log.Debug("no azure subscription id configured, will attempt to list all subscriptions")
		tAutorizer, err := auth.NewAuthorizerFromEnvironment()
		if err != nil {
			return nil, err
		}
		client := subscription.NewSubscriptionClient(tAutorizer)
		subscriptions, err := client.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		for _, subscription := range subscriptions {
			if subscription.SubscriptionID == nil {
				continue
			}
			log.Debugf("found azure subscription: %s", *subscription.SubscriptionID)
			// bit of hacking so we don't need to setup authorizer for each subscription
			// manually
			err := os.Setenv("AZURE_SUBSCRIPTION_ID", *subscription.SubscriptionID)
			if err != nil {
				log.WithError(err).Warn("failed to set azure subscription id")
				continue
			}

			authorizer, err := auth.NewAuthorizerFromEnvironment()
			if err != nil {
				log.WithError(err).Warn("failed to create azure authorizer")
				continue
			}
			list[*subscription.SubscriptionID] = authorizer
		}
	} else {
		authorizer, err := auth.NewAuthorizerFromEnvironment()
		if err != nil {
			return nil, err
		}
		list[c.Controller.AzureCredentials.SubscriptionID] = authorizer
		return list, nil
	}

	return nil, nil
}
