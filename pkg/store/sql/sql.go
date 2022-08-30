package sql

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/faroshq/faros/pkg/config"
	gormlogs "github.com/faroshq/faros/pkg/util/log/gorm"
)

type SQL struct{}

// Available DB types
const (
	DatabaseTypePostgres = "postgres"
	DatabaseTypeSqlite3  = sqlite.DriverName
)

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

			switch c.Database.Type {
			case DatabaseTypePostgres:
				dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable", c.Database.Host, c.Database.Username, c.Database.Password, c.Database.Name, c.Database.Port)
				dialector = postgres.Open(dsn)

				connConfig, err := pgxpool.ParseConfig(dsn)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to parse postgres config: %w", err)
				}
				connConfig.MaxConnIdleTime = c.Database.MaxConnIdleTime
				connConfig.MaxConnLifetime = c.Database.MaxConnLifeTime
				connConfig.MaxConns = 15

				pgxPool, err = pgxpool.ConnectConfig(ctx, connConfig)
				if err != nil {
					time.Sleep(1 * time.Second)
					log.WithError(err).Warn("sql store connector can't reach DB, waiting")
					continue
				}

			default:
				dialector = sqlite.Open(c.Database.SqliteURI)
			}
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
