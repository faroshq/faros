package storesql_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
	databasetest "github.com/faroshq/faros/test/util/database"
)

// TestCascade tests if records deletes cascades
func TestCascade(t *testing.T) {
	if os.Getenv("CI_ONLY") == "" {
		t.Skip("skipping postgres tests in non-CI environment")
		return
	}

	db, err := databasetest.NewPostgresTestingStore(t)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = db.CreateUser(ctx, models.User{
		User: tenancyv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{
				Name: "user.name",
			},
		},
	})
	require.NoError(t, err)

}
