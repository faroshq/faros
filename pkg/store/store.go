package store

import (
	"context"
	"errors"

	"github.com/faroshq/faros/pkg/models"
)

type Store interface {
	GetUser(context.Context, models.User) (*models.User, error)
	ListUsers(context.Context, models.User) ([]models.User, error)
	DeleteUser(context.Context, models.User) error
	CreateUser(context.Context, models.User) (*models.User, error)
	UpdateUser(context.Context, models.User) (*models.User, error)

	SubscribeChanges(ctx context.Context, callback func(event *models.Event) error) error

	// Status is a health check endpoint
	Status() (interface{}, error)
	RawDB() interface{}
	Close() error
}

var ErrFailToQuery = errors.New("malformed request. failed to query")
var ErrRecordNotFound = errors.New("object not found")
