package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load()
	assert.Equal(t, "50051", cfg.GRPCPort)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "development", cfg.Env)
	assert.Equal(t, 30*time.Second, cfg.ShutdownTimeout)
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("GRPC_PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ENV", "production")
	t.Setenv("SHUTDOWN_TIMEOUT", "1m")

	cfg := Load()
	assert.Equal(t, "9090", cfg.GRPCPort)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "production", cfg.Env)
	assert.Equal(t, time.Minute, cfg.ShutdownTimeout)
}

func TestShutdownTimeoutInvalidFallsBack(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "not-a-duration")
	cfg := Load()
	assert.Equal(t, 30*time.Second, cfg.ShutdownTimeout)
}
