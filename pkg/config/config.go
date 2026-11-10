package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Logger      LoggerConfig
	QueueConfig QueueConfig
	DB          DatabaseConfig
	Storage     StorageConfig
}

type LoggerConfig struct {
	Level      string
	OutputPath string
}

type QueueConfig struct {
	QueueAddr string
	QueueName string
}

type DatabaseConfig struct {
	ConnectionString string
	DbName           string
	MaxConnections   int
}

type StorageConfig struct {
	BucketName      string
	SecretAccessKey string
	AccessKeyID     string
	Region          string
	Url             string
}

func getEnv(key, fallback string) string {
	value, exists := os.LookupEnv(key)

	if !exists || value == "" {
		return fallback
	}

	return value
}

func LoadConfig() (*Config, error) {
	maxConnections, err := strconv.Atoi(getEnv("DB_MAX_CONNECTIONS", "10"))
	if err != nil {
		return nil, fmt.Errorf("parse DB_MAX_CONNECTIONS: %w", err)
	}

	return &Config{
		Logger: LoggerConfig{
			Level:      getEnv("LOG_LEVEL", "INFO"),
			OutputPath: getEnv("APP_LOG_PATH", ""),
		},

		QueueConfig: QueueConfig{
			QueueAddr: getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			QueueName: getEnv("RABBITMQ_QUEUE", "transcoder"),
		},

		DB: DatabaseConfig{
			ConnectionString: getEnv(
				"DATABASE_URL",
				"postgres://postgres:postgres@localhost:5432/transcoder_db?sslmode=disable",
			),
			DbName:         getEnv("DB_NAME", "transcoder_db"),
			MaxConnections: maxConnections,
		},

		Storage: StorageConfig{
			BucketName:      getEnv("STORAGE_BUCKET_NAME", ""),
			SecretAccessKey: getEnv("STORAGE_SECRET_ACCESS_KEY", ""),
			AccessKeyID:     getEnv("STORAGE_ACCESS_KEY_ID", ""),
			Region:          getEnv("STORAGE_REGION", "us-east-1"),
			Url:             getEnv("STORAGE_URL", ""),
		},
	}, nil
}
