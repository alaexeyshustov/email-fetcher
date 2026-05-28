package provider

import (
	"context"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
)

// Provider abstracts a single email provider adapter (Gmail, Yahoo Mail).
// The access token is passed per-call; no credentials are stored between calls.
type Provider interface {
	Name() string
	ListEmails(ctx context.Context, token string, format emailv1.EmailFormat, maxResults int32, pageToken string, labelIDs []string) ([]*emailv1.Email, *emailv1.PageInfo, error)
	SearchEmails(ctx context.Context, token string, query string, format emailv1.EmailFormat, maxResults int32, pageToken string) ([]*emailv1.Email, *emailv1.PageInfo, error)
	GetEmail(ctx context.Context, token string, id string, format emailv1.EmailFormat) (*emailv1.Email, error)
	GetLabels(ctx context.Context, token string) ([]*emailv1.Label, error)
	GetUnreadCount(ctx context.Context, token string, labelID string) (int32, error)
	ModifyLabels(ctx context.Context, token string, messageID string, addLabelIDs, removeLabelIDs []string) error
	GetAttachmentContent(ctx context.Context, token string, messageID, attachmentID string) (*emailv1.AttachmentContent, error)
}
