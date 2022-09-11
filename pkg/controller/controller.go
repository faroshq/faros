package controller

import (
	"context"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/session"
	"github.com/faroshq/faros/pkg/store"
	"github.com/sirupsen/logrus"
)

// Controller is controller server abstracting the storage and logic all together.
// This is so we can manipulate storage object between cloud providers and user provided data
// and still have consistent data model available for API.

type Controller interface {
	GetCluster(ctx context.Context, namespaceID, clusterID string) (*models.Cluster, error)
	CreateCluster(ctx context.Context, model models.Cluster) (*models.Cluster, error)
	DeleteCluster(ctx context.Context, clusterID string) error
	ListClusters(ctx context.Context, namespaceID string) ([]models.Cluster, error)
	UpdateCluster(ctx context.Context, model models.Cluster) (*models.Cluster, error)

	GetClusterAccessSession(ctx context.Context, query models.ClusterAccessSession) (*models.ClusterAccessSession, error)
	ListClusterAccessSessions(ctx context.Context, namespaceID, clusterID string) ([]models.ClusterAccessSession, error)
	DeleteClusterAccessSessions(ctx context.Context, sessionID string) error
	UpdateClusterAccessSession(ctx context.Context, session models.ClusterAccessSession) (*models.ClusterAccessSession, error)
	CreateClusterAccessSession(ctx context.Context, createClusterAccessSessionRequest models.ClusterAccessSession) (*models.ClusterAccessSession, error)

	ListNamespaces(ctx context.Context) ([]models.Namespace, error)
	GetNamespace(ctx context.Context, namespaceID string) (*models.Namespace, error)
	CreateNamespace(ctx context.Context, model models.Namespace) (*models.Namespace, error)
	DeleteNamespace(ctx context.Context, namespaceID string) error
	UpdateNamespace(ctx context.Context, model models.Namespace) (*models.Namespace, error)

	GetUser(ctx context.Context, model models.User) (*models.User, error)
	CreateUser(ctx context.Context, model models.User) (*models.User, error)
	ListUsers(ctx context.Context, model models.User) ([]models.User, error)
	DeleteUser(ctx context.Context, model models.User) error

	Run(ctx context.Context) error
}

type controller struct {
	log    *logrus.Entry
	config *config.Config
	store  store.Store

	sessions session.Interface
}

func New(log *logrus.Entry, config *config.Config, store store.Store) (*controller, error) {
	sessions, err := session.New(log, config, store)
	if err != nil {
		return nil, err
	}
	return &controller{
		log:    log.WithField("component", "controller"),
		config: config,
		store:  store,

		sessions: sessions,
	}, nil
}

func (c *controller) Run(ctx context.Context) error {
	return c.sessions.Run(ctx)
}
