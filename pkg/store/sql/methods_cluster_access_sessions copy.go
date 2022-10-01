package sql

import (
	"context"

	"github.com/pkg/errors"
	"gorm.io/gorm"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
)

// GetClusterRegistrationToken gets cluster registration token based on token objectID
func (s *Store) GetClusterRegistrationToken(ctx context.Context, p models.ClusterRegistrationToken) (*models.ClusterRegistrationToken, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	result := models.ClusterRegistrationToken{}
	if err := s.db.WithContext(ctx).Where(&p).First(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	return &result, nil
}

// CreateRegistrationToken creates cluster registration token
func (s *Store) CreateRegistrationToken(ctx context.Context, p models.ClusterRegistrationToken) (*models.ClusterRegistrationToken, error) {
	p.ID = models.NewClusterRegistrationTokenID()

	err := s.db.WithContext(ctx).Create(&p).Error
	if err != nil {
		return nil, err
	}
	return s.GetClusterRegistrationToken(ctx, models.ClusterRegistrationToken{ID: p.ID})
}

// DeleteRegistrationToken deletes cluster registration token
func (s *Store) DeleteRegistrationToken(ctx context.Context, p models.ClusterRegistrationToken) error {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return store.ErrFailToQuery
	}

	return s.db.WithContext(ctx).Delete(&p).Error
}

func (s *Store) ListRegistrationTokens(ctx context.Context, p models.ClusterRegistrationToken) ([]models.ClusterRegistrationToken, error) {
	switch {
	case p.NamespaceID != "":
		// OK, listing by NamespaceID
	default:
		return nil, store.ErrFailToQuery
	}

	result := []models.ClusterRegistrationToken{}
	if err := s.db.WithContext(ctx).Where(p).Find(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	return result, nil
}
