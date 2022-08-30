package sql_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

	namespace, err := db.CreateNamespace(ctx, models.Namespace{
		Name: "namespace.name",
	})
	require.NoError(t, err)

	cluster, err := db.CreateCluster(ctx, models.Cluster{
		Name:        "cluster.name",
		NamespaceID: namespace.ID,
	})
	require.NoError(t, err)

	access, err := db.CreateClusterAccessSession(ctx, models.ClusterAccessSession{
		NamespaceID: namespace.ID,
		ClusterID:   cluster.ID,
		Name:        "access.name",
		TTL:         time.Duration(time.Hour),
	})
	require.NoError(t, err)

	session, err := db.GetClusterAccessSession(ctx, models.ClusterAccessSession{
		ID: access.ID,
	})
	require.NoError(t, err)
	require.Equal(t, access.ID, session.ID)

	err = db.DeleteNamespace(ctx, *namespace)
	require.NoError(t, err)

	_, err = db.GetCluster(ctx, models.Cluster{
		ID: cluster.ID,
	})
	require.Error(t, err)

	_, err = db.GetClusterAccessSession(ctx, models.ClusterAccessSession{
		ID: access.ID,
	})
	require.Error(t, err)

}

// TestBadCreates checks if we can create resources using bad queries
func TestBadCreates(t *testing.T) {
	if os.Getenv("CI_ONLY") == "" {
		t.Skip("skipping postgres tests in non-CI environment")
		return
	}

	db, err := databasetest.NewPostgresTestingStore(t)
	require.NoError(t, err)

	ctx := context.Background()

	namespace, err := db.CreateNamespace(ctx, models.Namespace{
		Name: "namespace.name",
	})
	require.NoError(t, err)

	_, err = db.CreateCluster(ctx, models.Cluster{
		Name: "cluster.name",
	})
	require.Error(t, err)

	_, err = db.CreateClusterAccessSession(ctx, models.ClusterAccessSession{
		NamespaceID: namespace.ID,
		Name:        "access.name",
		TTL:         time.Duration(time.Hour),
	})
	require.Error(t, err)
}
