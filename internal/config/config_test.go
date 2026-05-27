package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load()
	assert.Equal(t, "50051", cfg.GRPCPort)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "development", cfg.Env)
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("GRPC_PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ENV", "production")

	cfg := Load()
	assert.Equal(t, "9090", cfg.GRPCPort)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "production", cfg.Env)
}
