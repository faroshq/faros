package sql

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// GetClusterAccessSession gets cluster access sessions based on clusterID and NamespaceID
func (s *Store) GetClusterAccessSession(ctx context.Context, p models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	result := models.ClusterAccessSession{}
	if err := s.db.WithContext(ctx).Where(&p).First(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}

	return &result, nil
}

// CreateClusterAccessSession creates cluster access session
func (s *Store) CreateClusterAccessSession(ctx context.Context, p models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	p.ID = models.NewClusterAccessSessionID()

	err := s.db.WithContext(ctx).Create(&p).Error
	if err != nil {
		return nil, err
	}
	return s.GetClusterAccessSession(ctx, models.ClusterAccessSession{ID: p.ID})
}

// UpdateClusterAccessSession updates cluster access session
func (s *Store) UpdateClusterAccessSession(ctx context.Context, p models.ClusterAccessSession) (*models.ClusterAccessSession, error) {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return nil, store.ErrFailToQuery
	}

	query := models.ClusterAccessSession{ID: p.ID, ClusterID: p.ClusterID, NamespaceID: p.NamespaceID}
	err := s.db.WithContext(ctx).Model(&models.Cluster{}).Where(&query).Save(&p).Error
	if err != nil {
		return nil, err
	}

	return s.GetClusterAccessSession(ctx, models.ClusterAccessSession{ID: p.ID, ClusterID: p.ClusterID, NamespaceID: p.NamespaceID})
}

// DeleteClusterAccessSession deletes cluster access session
func (s *Store) DeleteClusterAccessSession(ctx context.Context, p models.ClusterAccessSession) error {
	switch {
	case p.ID != "":
		// OK, getting by ID
	default:
		return store.ErrFailToQuery
	}

	return s.db.WithContext(ctx).Delete(&p).Error
}

func (s *Store) ListClusterAccessSessions(ctx context.Context, p models.ClusterAccessSession) ([]models.ClusterAccessSession, error) {
	switch {
	case p.NamespaceID != "" && p.ClusterID != "":
		// OK, listing by NamespaceID
	default:
		return nil, store.ErrFailToQuery
	}

	result := []models.ClusterAccessSession{}
	if err := s.db.WithContext(ctx).Where(&p).Find(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, store.ErrRecordNotFound
		}
		return nil, err
	}
	return result, nil
}
