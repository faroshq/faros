package sql

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/faroshq/faros/pkg/config"
	gormlogs "github.com/faroshq/faros/pkg/util/log/gorm"
)

type SQL struct {
	db *gorm.DB
}

// Available DB types
const (
	DatabaseTypePostgres = "postgres"
	DatabaseTypeSqlite3  = sqlite.DriverName
)

type scanner interface {
	Scan(...interface{}) error
}

func connect(ctx context.Context, log *logrus.Entry, c *config.Config) (*gorm.DB, *pgxpool.Pool, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("sql store startup deadline exceeded")
		default:

			var (
				err       error
				dialector gorm.Dialector
				pgxPool   *pgxpool.Pool
			)

			// TODO: This is the place to add support for other dialectors
			dialector = sqlite.Open(c.Database.SqliteURI)

			glogs := gormlogs.New(log)
			db, err := gorm.Open(dialector, &gorm.Config{
				Logger: glogs,
			})
			if err != nil {
				time.Sleep(1 * time.Second)
				log.Warnf("sql store connector can't reach DB, waiting: %s", err)
				continue
			}

			// TODO: Here we should set db overrides for pool, max connections, etc

			// success
			return db, pgxPool, nil

		}
	}
}

func createFK(db *gorm.DB, src, dst interface{}, fk, pk string, onDelete, onUpdate string) error {
	srcTableName := db.NamingStrategy.TableName(reflect.TypeOf(src).Name())
	dstTableName := db.NamingStrategy.TableName(reflect.TypeOf(dst).Name())

	constraintName := "fk_" + srcTableName + "_" + dstTableName

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if !db.Migrator().HasConstraint(src, constraintName) {
		err := db.WithContext(ctx).Exec(fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s(%s) ON DELETE %s ON UPDATE %s",
			srcTableName,
			constraintName,
			fk,
			dstTableName,
			pk,
			onDelete,
			onUpdate)).Error
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	return nil
}
