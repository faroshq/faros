package store

import (
	"context"
	"errors"

	"github.com/faroshq/faros/pkg/models"
)

type Store interface {
	GetCluster(context.Context, models.Cluster) (*models.Cluster, error)
	ListClusters(context.Context, models.Cluster) ([]models.Cluster, error)
	DeleteCluster(context.Context, models.Cluster) error
	CreateCluster(context.Context, models.Cluster) (*models.Cluster, error)
	UpdateCluster(context.Context, models.Cluster) (*models.Cluster, error)

	GetNamespace(context.Context, models.Namespace) (*models.Namespace, error)
	ListNamespaces(context.Context) ([]models.Namespace, error)
	DeleteNamespace(context.Context, models.Namespace) error
	CreateNamespace(context.Context, models.Namespace) (*models.Namespace, error)
	UpdateNamespace(context.Context, models.Namespace) (*models.Namespace, error)

	GetClusterAccessSession(context.Context, models.ClusterAccessSession) (*models.ClusterAccessSession, error)
	ListClusterAccessSessions(context.Context, models.ClusterAccessSession) ([]models.ClusterAccessSession, error)
	DeleteClusterAccessSession(context.Context, models.ClusterAccessSession) error
	CreateClusterAccessSession(context.Context, models.ClusterAccessSession) (*models.ClusterAccessSession, error)
	UpdateClusterAccessSession(context.Context, models.ClusterAccessSession) (*models.ClusterAccessSession, error)

	ListRegistrationTokens(context.Context, models.ClusterRegistrationToken) ([]models.ClusterRegistrationToken, error)
	DeleteRegistrationToken(context.Context, models.ClusterRegistrationToken) error
	GetClusterRegistrationToken(context.Context, models.ClusterRegistrationToken) (*models.ClusterRegistrationToken, error)
	CreateRegistrationToken(context.Context, models.ClusterRegistrationToken) (*models.ClusterRegistrationToken, error)

	GetUser(context.Context, models.User) (*models.User, error)
	ListUsers(context.Context, models.User) ([]models.User, error)
	DeleteUser(context.Context, models.User) error
	CreateUser(context.Context, models.User) (*models.User, error)
	UpdateUser(context.Context, models.User) (*models.User, error)

	// ListAllClusterAccessSessions will list all cluster sessions. Should not be used
	// in user context in any ways.
	ListAllClusterAccessSessions(context.Context) ([]models.ClusterAccessSession, error)

	// Status is a healthcheck endpoint
	Status() (interface{}, error)
	RawDB() interface{}
	Close() error
}

var ErrFailToQuery = errors.New("malformed request. failed to query")
var ErrRecordNotFound = errors.New("object not found")
