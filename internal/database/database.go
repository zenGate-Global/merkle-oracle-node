package database

import (
	"fmt"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/logging"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type Database struct {
	db     *gorm.DB
	logger *logging.Logger
}

func New(cfg *config.Config) (*Database, error) {
	logger := logging.GetLogger()
	// Open PostgreSQL DB
	db, err := gorm.Open(
		postgres.Open(cfg.Storage.URL),
		&gorm.Config{
			Logger: gormlogger.Discard,
		},
	)
	if err != nil {
		return nil, err
	}
	d := &Database{
		db:     db,
		logger: logger,
	}
	if err := Migrate(d.db); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	if err := d.LoadTrieFromDB(); err != nil {
		return nil, fmt.Errorf("failed to initialize trie: %w", err)
	}

	return d, nil
}

// DB returns the underlying GORM database instance
func (d *Database) DB() *gorm.DB {
	return d.db
}
