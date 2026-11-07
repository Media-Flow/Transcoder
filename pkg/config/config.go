package config

import "os"

type Config struct {
	Logger LoggerConfig
}

type LoggerConfig struct {
	Level      string
	OutputPath string
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
	}, nil
}
