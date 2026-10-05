package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const defaultMaxConns = 100

func ConnectPostgres() (*gorm.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}

	maxConns := defaultMaxConns
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid DB_MAX_CONNS %q", v)
		}
		maxConns = n
	}

	logLevel, err := gormLogLevel(os.Getenv("DB_LOG_LEVEL"))
	if err != nil {
		return nil, err
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		SkipDefaultTransaction: true,                             // transactions are managed explicitly; no implicit tx per write
		PrepareStmt:            true,                             // cache prepared statements per connection
		Logger:                 logger.Default.LogMode(logLevel), // silent by default: under load, printing slow queries costs CPU and skews results
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(maxConns)
	// Idle = max open, otherwise connections are closed and reopened after every burst.
	sqlDB.SetMaxIdleConns(maxConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	return db, nil
}

// LogPoolStats logs the connection pool's size and current use. Called once at
// startup, after Warm, to confirm the pool is the size you configured.
func LogPoolStats(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}

	s := sqlDB.Stats()
	slog.Info("db pool",
		"max_open_connections", s.MaxOpenConnections, // DB_MAX_CONNS: upper limit of the pool
		"open_connections", s.OpenConnections, // connections currently open (in use + idle)
		"in_use", s.InUse, // connections handed out to queries right now
		"idle", s.Idle, // open connections waiting to be reused
	)
	return nil
}

// gormLogLevel maps DB_LOG_LEVEL (silent, error, warn, info) to GORM's logger.
// Default is silent; booking errors are still logged by the HTTP handler.
func gormLogLevel(v string) (logger.LogLevel, error) {
	switch v {
	case "", "silent":
		return logger.Silent, nil
	case "error":
		return logger.Error, nil
	case "warn":
		return logger.Warn, nil
	case "info":
		return logger.Info, nil
	}
	return 0, fmt.Errorf("invalid DB_LOG_LEVEL %q (want silent, error, warn or info)", v)
}

// Warm opens every connection the pool allows so the sale spike does not pay
// connection setup cost. All connections are held at once (otherwise the pool
// would keep reusing one), then returned to the idle pool.
func Warm(ctx context.Context, gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}

	n := sqlDB.Stats().MaxOpenConnections
	conns := make([]*sql.Conn, 0, n)
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	for range n {
		c, err := sqlDB.Conn(ctx)
		if err != nil {
			return fmt.Errorf("warm connection %d/%d: %w", len(conns)+1, n, err)
		}
		conns = append(conns, c)
	}
	return nil
}
