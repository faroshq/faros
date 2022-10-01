package registrationtokens

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/faroshq/faros/pkg/cli/config"
	"github.com/faroshq/faros/pkg/cli/util/errors"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/stringutils"
)

func create(ctx context.Context, args []string, opts opts) error {
	c := &config.Config

	if opts.cluster == "" {
		fmt.Println("No cluster name provided, generating one...")
		opts.cluster = stringutils.GetRandomName()
	}
	fmt.Println("Cluster name:", opts.cluster)

	token := models.ClusterRegistrationToken{
		NamespaceID: c.Namespace,
		ClusterName: opts.cluster,
	}

	result, err := c.APIClient.CreateClusterRegistrationToken(ctx, token)
	if err != nil {
		return errors.ParseCloudError(err)
	}

	raw, err := base64.RawStdEncoding.DecodeString(result.Token)
	if err != nil {
		return err
	}

	fmt.Println("")
	fmt.Println("Cluster token:", string(raw))
	fmt.Println("")
	fmt.Println("Store token in the secure way. Token is valid for 1 cluster registration and will be deleted after registration.")
	fmt.Println("You will not able to get it again.")

	return nil
}
