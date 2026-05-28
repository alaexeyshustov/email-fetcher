package server_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
	"github.com/alaexeyshustov/email-fetcher/internal/fanout"
	"github.com/alaexeyshustov/email-fetcher/internal/provider"
	"github.com/alaexeyshustov/email-fetcher/internal/server"
)

// fakeProvider implements provider.Provider for server tests.
type fakeProvider struct {
	name                   string
	listEmailsFn           func(context.Context, string, emailv1.EmailFormat, int32, string, []string) ([]*emailv1.Email, *emailv1.PageInfo, error)
	searchEmailsFn         func(context.Context, string, string, emailv1.EmailFormat, int32, string) ([]*emailv1.Email, *emailv1.PageInfo, error)
	getEmailFn             func(context.Context, string, string, emailv1.EmailFormat) (*emailv1.Email, error)
	getLabelsFn            func(context.Context, string) ([]*emailv1.Label, error)
	getUnreadCountFn       func(context.Context, string, string) (int32, error)
	modifyLabelsFn         func(context.Context, string, string, []string, []string) error
	getAttachmentContentFn func(context.Context, string, string, string) (*emailv1.AttachmentContent, error)
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) ListEmails(ctx context.Context, token string, format emailv1.EmailFormat, max int32, pageToken string, labelIDs []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
	if f.listEmailsFn != nil {
		return f.listEmailsFn(ctx, token, format, max, pageToken, labelIDs)
	}
	return nil, &emailv1.PageInfo{}, nil
}

func (f *fakeProvider) SearchEmails(ctx context.Context, token, query string, format emailv1.EmailFormat, max int32, pageToken string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
	if f.searchEmailsFn != nil {
		return f.searchEmailsFn(ctx, token, query, format, max, pageToken)
	}
	return nil, &emailv1.PageInfo{}, nil
}

func (f *fakeProvider) GetEmail(ctx context.Context, token, id string, format emailv1.EmailFormat) (*emailv1.Email, error) {
	if f.getEmailFn != nil {
		return f.getEmailFn(ctx, token, id, format)
	}
	return &emailv1.Email{}, nil
}

func (f *fakeProvider) GetLabels(ctx context.Context, token string) ([]*emailv1.Label, error) {
	if f.getLabelsFn != nil {
		return f.getLabelsFn(ctx, token)
	}
	return nil, nil
}

func (f *fakeProvider) GetUnreadCount(ctx context.Context, token, labelID string) (int32, error) {
	if f.getUnreadCountFn != nil {
		return f.getUnreadCountFn(ctx, token, labelID)
	}
	return 0, nil
}

func (f *fakeProvider) ModifyLabels(ctx context.Context, token, messageID string, add, remove []string) error {
	if f.modifyLabelsFn != nil {
		return f.modifyLabelsFn(ctx, token, messageID, add, remove)
	}
	return nil
}

func (f *fakeProvider) GetAttachmentContent(ctx context.Context, token, messageID, attachmentID string) (*emailv1.AttachmentContent, error) {
	if f.getAttachmentContentFn != nil {
		return f.getAttachmentContentFn(ctx, token, messageID, attachmentID)
	}
	return &emailv1.AttachmentContent{}, nil
}

var _ provider.Provider = (*fakeProvider)(nil)

// setupServer wires a gRPC server over bufconn and returns a ready client.
func setupServer(t *testing.T, providers map[emailv1.Provider]provider.Provider) emailv1.EmailServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	emailv1.RegisterEmailServiceServer(srv, server.New(fanout.New(providers)))
	t.Cleanup(srv.GracefulStop)
	go srv.Serve(lis) //nolint:errcheck
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return emailv1.NewEmailServiceClient(conn)
}

// drainList receives all ListEmailsResponse messages from the stream.
func drainList(t *testing.T, stream emailv1.EmailService_ListEmailsClient) ([]*emailv1.Email, *emailv1.PageInfo) {
	t.Helper()
	var emails []*emailv1.Email
	var page *emailv1.PageInfo
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		switch p := resp.GetPayload().(type) {
		case *emailv1.ListEmailsResponse_Email:
			emails = append(emails, p.Email)
		case *emailv1.ListEmailsResponse_PageInfo:
			page = p.PageInfo
		}
	}
	return emails, page
}

// drainSearch receives all SearchEmailsResponse messages from the stream.
func drainSearch(t *testing.T, stream emailv1.EmailService_SearchEmailsClient) ([]*emailv1.Email, *emailv1.PageInfo) {
	t.Helper()
	var emails []*emailv1.Email
	var page *emailv1.PageInfo
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		switch p := resp.GetPayload().(type) {
		case *emailv1.SearchEmailsResponse_Email:
			emails = append(emails, p.Email)
		case *emailv1.SearchEmailsResponse_PageInfo:
			page = p.PageInfo
		}
	}
	return emails, page
}

// --- ListEmails ---

func TestListEmails_singleProvider(t *testing.T) {
	fake := &fakeProvider{
		name: "gmail",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, _ string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			return []*emailv1.Email{
				{Id: "INBOX/1", Subject: "Hello"},
			}, &emailv1.PageInfo{ResultCount: 1}, nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: fake,
	})

	stream, err := client.ListEmails(context.Background(), &emailv1.ListEmailsRequest{
		Credentials: []*emailv1.ProviderCredentials{{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"}},
	})
	require.NoError(t, err)

	emails, page := drainList(t, stream)
	require.Len(t, emails, 1)
	assert.Equal(t, "INBOX/1", emails[0].Id)
	require.NotNil(t, page)
	assert.Equal(t, int32(1), page.ResultCount)
}

func TestListEmails_fanOut(t *testing.T) {
	gmail := &fakeProvider{
		name: "gmail",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, _ string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			return []*emailv1.Email{{Id: "gmail/1"}}, &emailv1.PageInfo{ResultCount: 1}, nil
		},
	}
	yahoo := &fakeProvider{
		name: "yahoo",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, _ string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			return []*emailv1.Email{{Id: "INBOX/10"}}, &emailv1.PageInfo{ResultCount: 1}, nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: gmail,
		emailv1.Provider_YAHOO: yahoo,
	})

	stream, err := client.ListEmails(context.Background(), &emailv1.ListEmailsRequest{
		Credentials: []*emailv1.ProviderCredentials{
			{Provider: emailv1.Provider_GMAIL, AccessToken: "gtok"},
			{Provider: emailv1.Provider_YAHOO, AccessToken: "ytok"},
		},
	})
	require.NoError(t, err)

	emails, page := drainList(t, stream)
	assert.Len(t, emails, 2)
	require.NotNil(t, page)
	assert.Equal(t, int32(2), page.ResultCount)
}

func TestListEmails_partialFailure(t *testing.T) {
	gmail := &fakeProvider{
		name: "gmail",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, _ string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			return []*emailv1.Email{{Id: "gmail/1"}}, &emailv1.PageInfo{ResultCount: 1}, nil
		},
	}
	yahoo := &fakeProvider{
		name: "yahoo",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, _ string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			return nil, nil, errors.New("imap connection refused")
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: gmail,
		emailv1.Provider_YAHOO: yahoo,
	})

	var trailer metadata.MD
	stream, err := client.ListEmails(context.Background(), &emailv1.ListEmailsRequest{
		Credentials: []*emailv1.ProviderCredentials{
			{Provider: emailv1.Provider_GMAIL, AccessToken: "gtok"},
			{Provider: emailv1.Provider_YAHOO, AccessToken: "ytok"},
		},
	}, grpc.Trailer(&trailer))
	require.NoError(t, err)

	emails, _ := drainList(t, stream)
	assert.Len(t, emails, 1)
	assert.Equal(t, []string{"yahoo"}, trailer.Get("x-failed-providers"))
}

// --- SearchEmails ---

func TestSearchEmails(t *testing.T) {
	var gotQuery string
	fake := &fakeProvider{
		name: "gmail",
		searchEmailsFn: func(_ context.Context, _ string, query string, _ emailv1.EmailFormat, _ int32, _ string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			gotQuery = query
			return []*emailv1.Email{{Id: "gmail/5", Subject: "Invoice"}}, &emailv1.PageInfo{ResultCount: 1}, nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: fake,
	})

	stream, err := client.SearchEmails(context.Background(), &emailv1.SearchEmailsRequest{
		Credentials: []*emailv1.ProviderCredentials{{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"}},
		Query:       "invoice",
	})
	require.NoError(t, err)

	emails, page := drainSearch(t, stream)
	require.Len(t, emails, 1)
	assert.Equal(t, "Invoice", emails[0].Subject)
	require.NotNil(t, page)
	assert.Equal(t, int32(1), page.ResultCount)
	assert.Equal(t, "invoice", gotQuery)
}

// --- GetEmail ---

func TestGetEmail(t *testing.T) {
	fake := &fakeProvider{
		name: "gmail",
		getEmailFn: func(_ context.Context, _ string, id string, _ emailv1.EmailFormat) (*emailv1.Email, error) {
			return &emailv1.Email{Id: id, Subject: "Found"}, nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: fake,
	})

	resp, err := client.GetEmail(context.Background(), &emailv1.GetEmailRequest{
		Credentials: &emailv1.ProviderCredentials{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"},
		Id:          "INBOX/42",
		Format:      emailv1.EmailFormat_FULL,
	})
	require.NoError(t, err)
	assert.Equal(t, "INBOX/42", resp.GetEmail().GetId())
	assert.Equal(t, "Found", resp.GetEmail().GetSubject())
}

func TestGetEmail_unknownProvider(t *testing.T) {
	client := setupServer(t, map[emailv1.Provider]provider.Provider{})

	_, err := client.GetEmail(context.Background(), &emailv1.GetEmailRequest{
		Credentials: &emailv1.ProviderCredentials{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"},
		Id:          "INBOX/1",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- GetLabels ---

func TestGetLabels(t *testing.T) {
	fake := &fakeProvider{
		name: "gmail",
		getLabelsFn: func(_ context.Context, _ string) ([]*emailv1.Label, error) {
			return []*emailv1.Label{
				{Id: "INBOX", Name: "Inbox", Type: emailv1.LabelType_SYSTEM},
			}, nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: fake,
	})

	resp, err := client.GetLabels(context.Background(), &emailv1.GetLabelsRequest{
		Credentials: &emailv1.ProviderCredentials{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"},
	})
	require.NoError(t, err)
	require.Len(t, resp.GetLabels(), 1)
	assert.Equal(t, "INBOX", resp.GetLabels()[0].GetId())
}

// --- GetUnreadCount ---

func TestGetUnreadCount(t *testing.T) {
	fake := &fakeProvider{
		name: "yahoo",
		getUnreadCountFn: func(_ context.Context, _ string, labelID string) (int32, error) {
			assert.Equal(t, "INBOX", labelID)
			return 7, nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_YAHOO: fake,
	})

	labelID := "INBOX"
	resp, err := client.GetUnreadCount(context.Background(), &emailv1.GetUnreadCountRequest{
		Credentials: &emailv1.ProviderCredentials{Provider: emailv1.Provider_YAHOO, AccessToken: "tok"},
		LabelId:     &labelID,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(7), resp.GetCount())
}

// --- ModifyLabels ---

func TestModifyLabels(t *testing.T) {
	var gotAdd, gotRemove []string
	fake := &fakeProvider{
		name: "yahoo",
		modifyLabelsFn: func(_ context.Context, _ string, messageID string, add, remove []string) error {
			gotAdd = add
			gotRemove = remove
			return nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_YAHOO: fake,
	})

	_, err := client.ModifyLabels(context.Background(), &emailv1.ModifyLabelsRequest{
		Credentials:    &emailv1.ProviderCredentials{Provider: emailv1.Provider_YAHOO, AccessToken: "tok"},
		MessageId:      "INBOX/42",
		AddLabelIds:    []string{"Archive"},
		RemoveLabelIds: []string{"INBOX"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Archive"}, gotAdd)
	assert.Equal(t, []string{"INBOX"}, gotRemove)
}

// --- GetAttachmentContent ---

func TestGetAttachmentContent(t *testing.T) {
	fake := &fakeProvider{
		name: "gmail",
		getAttachmentContentFn: func(_ context.Context, _ string, messageID, attachmentID string) (*emailv1.AttachmentContent, error) {
			assert.Equal(t, "msg123", messageID)
			assert.Equal(t, "att456", attachmentID)
			return &emailv1.AttachmentContent{Content: []byte("pdf data")}, nil
		},
	}
	client := setupServer(t, map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: fake,
	})

	resp, err := client.GetAttachmentContent(context.Background(), &emailv1.GetAttachmentContentRequest{
		Credentials:  &emailv1.ProviderCredentials{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"},
		MessageId:    "msg123",
		AttachmentId: "att456",
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("pdf data"), resp.GetContent().GetContent())
}
