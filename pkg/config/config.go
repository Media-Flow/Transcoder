package config

import "os"

type Config struct {
	Logger      LoggerConfig
	QueueConfig QueueConfig
}

type LoggerConfig struct {
	Level      string
	OutputPath string
}

type QueueConfig struct {
	QueueAddr string
	QueueName string
}

func getEnv(key, fallback string) string {
	value, exists := os.LookupEnv(key)

	if !exists || value == "" {
		return fallback
	}

	return value
}

func LoadConfig() (*Config, error) {
	return &Config{
		Logger: LoggerConfig{
			Level:      getEnv("LOG_LEVEL", "INFO"),
			OutputPath: getEnv("APP_LOG_PATH", ""),
		},
		QueueConfig: QueueConfig{
			QueueAddr: getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			QueueName: getEnv("RABBITMQ_QUEUE", "transcoder"),
		},
	}, nil
}
