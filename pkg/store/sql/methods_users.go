package sql

import (
	"context"

	"github.com/pkg/errors"
	"gorm.io/gorm"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
)

// GetUser gets full user based on args user
// Search: ID or Name and Namespace must be provided
func (s *Store) GetUser(ctx context.Context, p models.User) (*models.User, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	case p.Email != "" && p.ProviderName == models.AuthenticationProviderBasicAuth:
		// Ok getting my email for basic auth
	default:
		return nil, store.ErrFailToQuery
	}

	result := models.User{}
	if err := s.db.WithContext(ctx).Where(&p).First(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	return &result, nil
}

// CreateUser creates user and assigns unique ID
func (s *Store) CreateUser(ctx context.Context, p models.User) (*models.User, error) {
	p.ID = models.NewUserID()

	err := s.db.WithContext(ctx).Create(&p).Error
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, models.User{ID: p.ID})
}

// UpdateUser updates user based on user ID
func (s *Store) UpdateUser(ctx context.Context, p models.User) (*models.User, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	query := models.User{ID: p.ID}
	err := s.db.WithContext(ctx).Model(&models.Cluster{}).Where(&query).Save(&p).Error
	if err != nil {
		return nil, err
	}

	return s.GetUser(ctx, models.User{ID: p.ID})
}

// DeleteUser deletes user based on user ID
func (s *Store) DeleteUser(ctx context.Context, p models.User) error {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return store.ErrFailToQuery
	}

	return s.db.WithContext(ctx).Delete(&p).Error
}

func (s *Store) ListUsers(ctx context.Context, p models.User) ([]models.User, error) {
	results := []models.User{}
	if err := s.db.WithContext(ctx).Where(&p).Find(&results).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	for idx := range results {
		results[idx].PasswordHash = "redacted"
	}

	return results, nil
}
