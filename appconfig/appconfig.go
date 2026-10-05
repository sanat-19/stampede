package appconfig

import (
	"fmt"
	"log"
	"os"
	"stampede/db"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

type Config struct {
	HTTPAddr            string
	PprofAddr           string
	EventID             int64
	RequestTimeout      time.Duration // deadline for the whole booking: pool acquire + transaction
	SoldOutShortCircuit bool
	LogRequests         bool // log one line per booking with its timing (off by default: costly under load)
}

var (
	DB  *gorm.DB
	Cfg Config
)

func AppConfig() error {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on real environment variables")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	conn, err := db.ConnectPostgres()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	Cfg = cfg
	DB = conn
	return nil
}

func loadConfig() (Config, error) {
	var (
		cfg Config
		err error
	)

	cfg.HTTPAddr = envString("HTTP_ADDR", ":8080")
	cfg.PprofAddr = envString("PPROF_ADDR", ":6060")

	if cfg.EventID, err = envInt64("EVENT_ID", 1); err != nil {
		return Config{}, err
	}
	if cfg.RequestTimeout, err = envDuration("REQUEST_TIMEOUT", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.SoldOutShortCircuit, err = envBool("SOLD_OUT_SHORT_CIRCUIT", true); err != nil {
		return Config{}, err
	}
	if cfg.LogRequests, err = envBool("LOG_REQUESTS", false); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt64(key string, def int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, v, err)
	}
	return n, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid %s %q", key, v)
	}
	return d, nil
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q: %w", key, v, err)
	}
	return b, nil
}
