package config

import (
	"os"
	"time"
)

type Config struct {
	GRPCPort        string
	LogLevel        string
	Env             string
	ShutdownTimeout time.Duration
}

func Load() Config {
	return Config{
		GRPCPort:        getEnv("GRPC_PORT", "50051"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		Env:             getEnv("ENV", "development"),
		ShutdownTimeout: getDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
