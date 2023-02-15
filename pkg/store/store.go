package store

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type Store interface {
	GetUser(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.User, error)
	ListUsers(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.UserList, error)
	DeleteUser(context.Context, tenancyv1alpha1.User) error
	CreateUser(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.User, error)
	UpdateUser(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.User, error)
}
