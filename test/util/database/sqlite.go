package database

import (
	"os"
	"testing"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/store/sql"
	logger "github.com/faroshq/faros/pkg/util/log"
)

// NewSQLLiteTestingStore creates a new sqllite database
func NewSQLLiteTestingStore(t *testing.T) (store.Store, error) {
	log := logger.GetLogger()
	t.Log("using sqllite database")

	// Setting defaults if nothing is set so it works
	// with local postgres created by docker-compose
	if os.Getenv("FAROS_DATABASE_TYPE") == "" {
		os.Setenv("FAROS_DATABASE_TYPE", "sqllite")
		os.Setenv("FAROS_DATABASE_SQLITE_URI", "file::memory:?cache=shared")
	}

	var store store.Store
	var err error

	cfg, err := config.Load(false)
	if err != nil {
		return nil, err
	}

	// connecting to test DB
	store, err = sql.NewStore(log, cfg)
	if err != nil {
		return nil, err
	}

	return store, nil
}
