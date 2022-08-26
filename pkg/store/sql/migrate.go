package sql

import (
	"context"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/models"
)

func (s *Store) migrate(_ context.Context, c *config.Config) error {
	err := s.db.AutoMigrate(
		&models.Cluster{},
		&models.Namespace{},
		&models.ClusterAccessSession{},
	)
	if err != nil {
		return err
	}

	if c.Database.Type == DatabaseTypePostgres {
		if err := createFK(s.db, models.Cluster{}, models.Namespace{}, "namespace_id", "id", "CASCADE", "CASCADE"); err != nil {
			s.log.Warnf("failed to add DB FK: %s", err)
		}

		if err := createFK(s.db, models.ClusterAccessSession{}, models.Namespace{}, "namespace_id", "id", "CASCADE", "CASCADE"); err != nil {
			s.log.Warnf("failed to add DB FK: %s", err)
		}

		if err := createFK(s.db, models.ClusterAccessSession{}, models.Cluster{}, "cluster_id", "id", "CASCADE", "CASCADE"); err != nil {
			s.log.Warnf("failed to add DB FK: %s", err)
		}

	}
	return nil
}
