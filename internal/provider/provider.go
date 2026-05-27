package provider

import "context"

// Credentials carries the OAuth access token passed per-request from Rails.
// The service is stateless — tokens are never stored.
type Credentials struct {
	AccessToken string
}

// Provider is the interface implemented by each email provider adapter (Gmail, Yahoo Mail).
// Methods will be expanded once proto code generation is complete.
type Provider interface {
	Name() string
	Ping(ctx context.Context, creds Credentials) error
}
