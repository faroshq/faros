package sql

import (
	"context"
	"fmt"
	"time"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/store"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var _ store.Store = &Store{}

type Store struct {
	log     *logrus.Entry
	db      *gorm.DB
	pgxPool *pgxpool.Pool // used for pubsub if we need one
}

func NewStore(log *logrus.Entry, c *config.Config) (*Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	log = log.WithField("database", sqlite.DriverName)
	db, pgxPool, err := connect(ctx, log, c)
	if err != nil {
		return nil, err
	}

	log.WithField("dialector", db.Dialector.Name()).Info("Initializing database store")

	if db.Dialector.Name() == sqlite.DriverName {
		err = db.Exec("PRAGMA foreign_keys = ON").Error
		if err != nil {
			return nil, err
		}
	}

	s := &Store{
		log:     log,
		db:      db,
		pgxPool: pgxPool,
	}

	err = s.migrate(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("database migration failed: %w", err)
	}

	return s, nil
}

func (s *Store) Status() (interface{}, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	db, err := s.db.DB()
	if err != nil {
		return nil, err
	}

	err = db.Ping()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (s *Store) Close() error {
	if s.pgxPool != nil {
		s.pgxPool.Close()
	}
	db, err := s.db.DB()
	if err != nil {
		return nil
	}
	return db.Close()
}
