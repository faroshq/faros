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
func (s *Store) GetCluster(ctx context.Context, p models.Cluster) (*models.Cluster, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	result := models.Cluster{}
	if err := s.db.WithContext(ctx).Where(&p).First(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	// decryt kubeconfig
	if s.encryptionEnabled {
		var err error
		result.Config.RawKubeConfig, err = s.encryption.Open(result.Config.RawKubeConfig)
		if err != nil {
			return nil, err
		}
	}

	return &result, nil
}

// CreateUser creates user and assigns unique ID
func (s *Store) CreateCluster(ctx context.Context, p models.Cluster) (*models.Cluster, error) {
	p.ID = models.NewClusterID()

	if s.encryptionEnabled {
		var err error
		p.Config.RawKubeConfig, err = s.encryption.Seal(p.Config.RawKubeConfig)
		if err != nil {
			return nil, err
		}
	}

	err := s.db.WithContext(ctx).Create(&p).Error
	if err != nil {
		return nil, err
	}
	return s.GetCluster(ctx, models.Cluster{ID: p.ID})
}

// UpdateUser updates user based on user ID
func (s *Store) UpdateCluster(ctx context.Context, p models.Cluster) (*models.Cluster, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	if s.encryptionEnabled && p.Config.RawKubeConfig != "" {
		var err error
		p.Config.RawKubeConfig, err = s.encryption.Seal(p.Config.RawKubeConfig)
		if err != nil {
			return nil, err
		}
	}

	query := models.Cluster{ID: p.ID, Name: p.Name, NamespaceID: p.NamespaceID}
	err := s.db.WithContext(ctx).Model(&models.Cluster{}).Where(&query).Save(&p).Error
	if err != nil {
		return nil, err
	}

	return s.GetCluster(ctx, models.Cluster{ID: p.ID, Name: p.Name, NamespaceID: p.NamespaceID})
}

// DeleteUser deletes user based on user ID
func (s *Store) DeleteCluster(ctx context.Context, p models.Cluster) error {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return store.ErrFailToQuery
	}

	return s.db.WithContext(ctx).Delete(&p).Error
}

func (s *Store) ListClusters(ctx context.Context, p models.Cluster) ([]models.Cluster, error) {
	switch {
	case p.NamespaceID != "":
		// OK, listing by NamespaceID
	default:
		return nil, store.ErrFailToQuery
	}

	results := []models.Cluster{}
	if err := s.db.WithContext(ctx).Where(&p).Find(&results).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	for idx := range results {
		results[idx].Config.RawKubeConfig = "redacted"
	}

	return results, nil
}
