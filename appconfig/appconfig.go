package appconfig

import (
	"fmt"
	"log"
	"stampede/db"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

var (
	DB *gorm.DB
)

func AppConfig() error {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on real environment variables")
	}

	conn, err := db.ConnectPostgres()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	DB = conn
	return nil
}
