package registrationtokens

import (
	"context"
	"strconv"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/cli/util/formater"
	printutil "github.com/faroshq/faros/pkg/cli/util/print"
	"github.com/faroshq/faros/pkg/models"
)

func list(ctx context.Context, opts opts) error {
	c := &config.Config

	namespaces, err := c.APIClient.ListNamespaces(ctx)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	tokens, err := c.APIClient.ListClusterRegistrationTokens(ctx, models.ClusterRegistrationToken{
		NamespaceID: c.Namespace,
	})
	if err != nil {
		return errors.ParseCloudError(err)
	}

	var tokensList []struct {
		models.ClusterRegistrationToken
		Namespace string
	}

	for _, token := range tokens {
		for _, namespace := range namespaces {
			if token.NamespaceID == namespace.ID {
				tokensList = append(tokensList, struct {
					models.ClusterRegistrationToken
					Namespace string
				}{
					token,
					namespace.Name,
				})
			}
		}
	}

	if c.Output == printutil.FormatTable {
		table := printutil.DefaultTable()
		table.SetHeader([]string{"Name", "Namespace", "Created", "Updated", "TTL", "Expired"})
		for _, c := range tokensList {
			createdStr := formater.Since(c.CreatedAt).String() + " ago"
			updatedStr := formater.Since(c.UpdatedAt).String() + " ago"

			table.Append([]string{
				c.Name,
				c.ClusterName,
				c.Namespace,
				createdStr,
				updatedStr,
				c.TTL.String(),
				strconv.FormatBool(c.Expired),
			})
		}

		table.Render()
		return nil
	}

	return printutil.PrintWithFormat(sessionsList, c.Output)
}
