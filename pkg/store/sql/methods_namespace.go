package sql

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// GetNamespace gets full user based on args user
// Search: ID or Name must be provided
func (s *Store) GetNamespace(ctx context.Context, p models.Namespace) (*models.Namespace, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	result := models.Namespace{}
	if err := s.db.WithContext(ctx).Where(&p).First(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	return &result, nil
}

// CreateNamespace creates Namespace and assigns unique ID
func (s *Store) CreateNamespace(ctx context.Context, p models.Namespace) (*models.Namespace, error) {
	p.ID = models.NewNamespaceID()

	err := s.db.WithContext(ctx).Create(&p).Error
	if err != nil {
		return nil, err
	}
	return s.GetNamespace(ctx, models.Namespace{ID: p.ID})
}

// UpdateNamespace updates Namespace on Namespace ID
func (s *Store) UpdateNamespace(ctx context.Context, p models.Namespace) (*models.Namespace, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	query := models.Namespace{ID: p.ID, Name: p.Name}
	err := s.db.WithContext(ctx).Model(&models.Namespace{}).Where(&query).Save(&p).Error
	if err != nil {
		return nil, err
	}

	return s.GetNamespace(ctx, models.Namespace{ID: p.ID, Name: p.Name})
}

// DeleteNamespace Namespace based on Namespace ID
func (s *Store) DeleteNamespace(ctx context.Context, p models.Namespace) error {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return store.ErrFailToQuery
	}

	return s.db.WithContext(ctx).Delete(&p).Error
}

func (s *Store) ListNamespaces(ctx context.Context) ([]models.Namespace, error) {
	result := []models.Namespace{}
	if err := s.db.WithContext(ctx).Find(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}
	return result, nil
}
