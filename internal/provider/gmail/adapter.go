package gmail

import (
	"context"
	"encoding/base64"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
)

// Adapter implements provider.Provider for Gmail using the REST API v1.
type Adapter struct {
	baseURL string // overridden in tests via WithBaseURL
}

// Option configures the Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the Gmail API base URL. Used in tests to point at a fake server.
func WithBaseURL(url string) Option {
	return func(a *Adapter) { a.baseURL = url }
}

func New(opts ...Option) *Adapter {
	a := &Adapter{}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *Adapter) Name() string { return "gmail" }

func (a *Adapter) newService(ctx context.Context, token string) (*gmailapi.Service, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	opts := []option.ClientOption{option.WithTokenSource(ts)}
	if a.baseURL != "" {
		opts = append(opts, option.WithEndpoint(a.baseURL))
	}
	svc, err := gmailapi.NewService(ctx, opts...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "gmail: failed to create service: %v", err)
	}
	return svc, nil
}

func (a *Adapter) GetEmail(ctx context.Context, token string, id string, format emailv1.EmailFormat) (*emailv1.Email, error) {
	svc, err := a.newService(ctx, token)
	if err != nil {
		return nil, err
	}
	return a.fetchMessage(ctx, svc, id, format)
}

func (a *Adapter) ListEmails(ctx context.Context, token string, format emailv1.EmailFormat, maxResults int32, pageToken string, labelIDs []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
	svc, err := a.newService(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	call := svc.Users.Messages.List("me").Context(ctx)
	if maxResults > 0 {
		call = call.MaxResults(int64(maxResults))
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	if len(labelIDs) > 0 {
		call = call.LabelIds(labelIDs...)
	}
	res, err := call.Do()
	if err != nil {
		return nil, nil, mapError(err)
	}
	emails, err := a.fetchMessages(ctx, svc, res.Messages, format)
	if err != nil {
		return nil, nil, err
	}
	return emails, &emailv1.PageInfo{
		NextPageToken: res.NextPageToken,
		ResultCount:   int32(len(emails)),
	}, nil
}

func (a *Adapter) SearchEmails(ctx context.Context, token string, query string, format emailv1.EmailFormat, maxResults int32, pageToken string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
	svc, err := a.newService(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	call := svc.Users.Messages.List("me").Context(ctx).Q(query)
	if maxResults > 0 {
		call = call.MaxResults(int64(maxResults))
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	res, err := call.Do()
	if err != nil {
		return nil, nil, mapError(err)
	}
	emails, err := a.fetchMessages(ctx, svc, res.Messages, format)
	if err != nil {
		return nil, nil, err
	}
	return emails, &emailv1.PageInfo{
		NextPageToken: res.NextPageToken,
		ResultCount:   int32(len(emails)),
	}, nil
}

func (a *Adapter) GetLabels(ctx context.Context, token string) ([]*emailv1.Label, error) {
	svc, err := a.newService(ctx, token)
	if err != nil {
		return nil, err
	}
	res, err := svc.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		return nil, mapError(err)
	}
	labels := make([]*emailv1.Label, len(res.Labels))
	for i, l := range res.Labels {
		labels[i] = toLabel(l)
	}
	return labels, nil
}

func (a *Adapter) GetUnreadCount(ctx context.Context, token string, labelID string) (int32, error) {
	svc, err := a.newService(ctx, token)
	if err != nil {
		return 0, err
	}
	if labelID != "" {
		l, err := svc.Users.Labels.Get("me", labelID).Context(ctx).Do()
		if err != nil {
			return 0, mapError(err)
		}
		return int32(l.MessagesUnread), nil
	}
	// No specific label: ResultSizeEstimate is approximate but acceptable for V1.
	res, err := svc.Users.Messages.List("me").Context(ctx).Q("is:unread").MaxResults(1).Do()
	if err != nil {
		return 0, mapError(err)
	}
	return int32(res.ResultSizeEstimate), nil
}

func (a *Adapter) ModifyLabels(ctx context.Context, token string, messageID string, addLabelIDs, removeLabelIDs []string) error {
	svc, err := a.newService(ctx, token)
	if err != nil {
		return err
	}
	_, err = svc.Users.Messages.Modify("me", messageID, &gmailapi.ModifyMessageRequest{
		AddLabelIds:    addLabelIDs,
		RemoveLabelIds: removeLabelIDs,
	}).Context(ctx).Do()
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (a *Adapter) GetAttachmentContent(ctx context.Context, token string, messageID, attachmentID string) (*emailv1.AttachmentContent, error) {
	svc, err := a.newService(ctx, token)
	if err != nil {
		return nil, err
	}
	att, err := svc.Users.Messages.Attachments.Get("me", messageID, attachmentID).Context(ctx).Do()
	if err != nil {
		return nil, mapError(err)
	}
	data, err := base64.RawURLEncoding.DecodeString(att.Data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "gmail: failed to decode attachment: %v", err)
	}
	// Gmail's attachment endpoint does not return mime_type.
	// Callers should use the MimeType from Attachment metadata already present on Email.
	return &emailv1.AttachmentContent{Content: data}, nil
}

func (a *Adapter) fetchMessages(ctx context.Context, svc *gmailapi.Service, stubs []*gmailapi.Message, format emailv1.EmailFormat) ([]*emailv1.Email, error) {
	emails := make([]*emailv1.Email, 0, len(stubs))
	for _, m := range stubs {
		email, err := a.fetchMessage(ctx, svc, m.Id, format)
		if err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, nil
}

func (a *Adapter) fetchMessage(ctx context.Context, svc *gmailapi.Service, id string, format emailv1.EmailFormat) (*emailv1.Email, error) {
	call := svc.Users.Messages.Get("me", id).Context(ctx)
	switch format {
	case emailv1.EmailFormat_METADATA:
		call = call.Format("metadata").MetadataHeaders("Subject", "From", "To", "Date")
	case emailv1.EmailFormat_MINIMAL:
		call = call.Format("minimal")
	default:
		call = call.Format("full")
	}
	msg, err := call.Do()
	if err != nil {
		return nil, mapError(err)
	}
	return toEmail(msg), nil
}
