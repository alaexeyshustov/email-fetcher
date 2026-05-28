package fanout

import (
	"encoding/base64"
	"encoding/json"
)

// compositeCursor maps provider name → per-provider page token.
// Encoded as base64(JSON) and surfaced as PageInfo.next_page_token.
type compositeCursor map[string]string

func parseCursor(token string) compositeCursor {
	if token == "" {
		return compositeCursor{}
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return compositeCursor{}
	}
	var c compositeCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return compositeCursor{}
	}
	return c
}

// BuildCompositeToken encodes per-provider page tokens into a single opaque
// cursor. An empty map produces an empty string (no next page).
func BuildCompositeToken(tokens map[string]string) string {
	if len(tokens) == 0 {
		return ""
	}
	data, _ := json.Marshal(tokens)
	return base64.RawURLEncoding.EncodeToString(data)
}

func (c compositeCursor) get(name string) string { return c[name] }
