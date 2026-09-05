package database

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/barlowtj48/names/shared/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func ConnectDatabase(host, user, password, dbname, port, sslmode, timezone, env string) error {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=%s",
		host, user, password, dbname, port, sslmode, timezone)

	for range 10 {
		fmt.Println("Connecting to database...")
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			DB = db
			break
		}
		fmt.Println("Error connecting to database:", err)
		fmt.Println("Retrying to connect to database...")
		time.Sleep(5 * time.Second)
	}
	if DB == nil {
		return fmt.Errorf("failed to connect to database after 10 retries")
	}

	fmt.Println("Database connection established.")
	// LogMode returns a new logger; it must be assigned back to take effect.
	// Expected misses (First on a bad id → 404) are not worth a log line.
	if env == "production" {
		DB.Logger = DB.Logger.LogMode(logger.Silent)
	} else {
		DB.Logger = logger.New(log.New(os.Stdout, "", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		})
	}
	return nil
}

func MigrateDatabase() error {
	modelsToMigrate := []any{
		&models.Name{},
		&models.Vote{},
		&models.NameFlag{},
	}
	for _, m := range modelsToMigrate {
		if err := DB.AutoMigrate(m); err != nil {
			return fmt.Errorf("auto-migrate %T: %w", m, err)
		}
	}
	return nil
}
