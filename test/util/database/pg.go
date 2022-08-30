package database

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/store/sql"
	logger "github.com/faroshq/faros/pkg/util/log"
	"github.com/sirupsen/logrus"
)

// NewPostgresTestingStore creates a new, clean test database for the current
// test and drops it on test cleanup.
func NewPostgresTestingStore(t *testing.T) (store.Store, error) {
	log := logger.GetLogger()
	t.Log("using postgres database")

	// Setting defaults if nothing is set so it works
	// with local postgres created by docker-compose
	if os.Getenv("FAROS_DATABASE_TYPE") == "" {
		os.Setenv("FAROS_DATABASE_TYPE", "postgres")
		os.Setenv("FAROS_DATABASE_HOST", "localhost")
		os.Setenv("FAROS_DATABASE_PASSWORD", "pgpass")
		os.Setenv("FAROS_DATABASE_USERNAME", "pguser")
	}

	var store store.Store
	var err error

	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	testDatabaseName := fmt.Sprintf("test%d", time.Now().UnixNano())

	t.Cleanup(func() {
		store.Close()

		cfg.Database.Name = "postgres"

		store, err := sql.NewStore(log, cfg)
		if err != nil {
			log.WithError(err).Fatal("failed to connect to postgres database, is it running?")
		}
		defer store.Close()

		db := store.RawDB().(*gorm.DB)
		err = db.Exec(fmt.Sprintf("drop database %s", testDatabaseName)).Error
		if err != nil {
			log.WithError(err).Error("failed to drop test database")
		}

	})

	err = createTestDatabase(log, cfg, testDatabaseName)
	if err != nil {
		return nil, err
	}

	// connecting to test DB
	cfg.Database.Name = testDatabaseName
	store, err = sql.NewStore(log, cfg)
	if err != nil {
		return nil, err
	}

	return store, nil
}

func createTestDatabase(log *logrus.Entry, cfg *config.Config, testDatabaseName string) error {
	cfg.Database.Name = "postgres"

	store, err := sql.NewStore(log, cfg)
	if err != nil {
		log.WithError(err).Fatal("failed to connect to postgres database, is it running?")
	}
	defer store.Close()

	db := store.RawDB().(*gorm.DB)
	err = db.Exec(fmt.Sprintf("create database %s", testDatabaseName)).Error
	if err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	return nil
}
