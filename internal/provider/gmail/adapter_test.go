package gmail_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
	"github.com/alaexeyshustov/email-fetcher/internal/provider/gmail"
)

func encodeBase64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// serve registers handler on mux and returns a started test server + adapter pointed at it.
func newTestAdapter(t *testing.T, mux *http.ServeMux) *gmail.Adapter {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return gmail.New(gmail.WithBaseURL(srv.URL + "/"))
}

// gmailErrBody returns a Gmail-style JSON error response body.
func gmailErrBody(code int, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"errors":  []any{},
		},
	})
	return b
}

// --- ListEmails ---

func TestAdapter_ListEmails(t *testing.T) {
	mux := http.NewServeMux()
	// messages.list returns stubs
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gmailapi.ListMessagesResponse{
			Messages:      []*gmailapi.Message{{Id: "msg1"}, {Id: "msg2"}},
			NextPageToken: "tok_page2",
		})
	})
	// messages.get for each stub
	for _, id := range []string{"msg1", "msg2"} {
		id := id
		mux.HandleFunc("/gmail/v1/users/me/messages/"+id, func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(gmailapi.Message{
				Id: id, Snippet: "snippet " + id,
				Payload: &gmailapi.MessagePart{
					Headers: []*gmailapi.MessagePartHeader{{Name: "Subject", Value: "Sub " + id}},
				},
			})
		})
	}

	adapter := newTestAdapter(t, mux)
	emails, page, err := adapter.ListEmails(context.Background(), "token", emailv1.EmailFormat_METADATA, 10, "", nil)

	require.NoError(t, err)
	require.Len(t, emails, 2)
	assert.Equal(t, "msg1", emails[0].Id)
	assert.Equal(t, "msg2", emails[1].Id)
	assert.Equal(t, "tok_page2", page.NextPageToken)
	assert.Equal(t, int32(2), page.ResultCount)
}

// --- GetAttachmentContent ---

func TestAdapter_GetAttachmentContent(t *testing.T) {
	// "PDF content" base64url-encoded (no padding)
	encoded := "UERGIGN jb250ZW50" // will use proper encoding below
	rawBytes := []byte("PDF content")

	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/msg1/attachments/att1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": encodeBase64URL(rawBytes),
			"size": len(rawBytes),
		})
	})

	adapter := newTestAdapter(t, mux)
	content, err := adapter.GetAttachmentContent(context.Background(), "token", "msg1", "att1")

	_ = encoded
	require.NoError(t, err)
	assert.Equal(t, rawBytes, content.Content)
	assert.Empty(t, content.MimeType) // Gmail attachment endpoint does not return mime_type
}

// --- ModifyLabels ---

func TestAdapter_ModifyLabels(t *testing.T) {
	type modifyReq struct {
		AddLabelIds    []string `json:"addLabelIds"`
		RemoveLabelIds []string `json:"removeLabelIds"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/msg1/modify", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		var body modifyReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"STARRED"}, body.AddLabelIds)
		assert.Equal(t, []string{"UNREAD"}, body.RemoveLabelIds)
		json.NewEncoder(w).Encode(gmailapi.Message{Id: "msg1"})
	})

	adapter := newTestAdapter(t, mux)
	err := adapter.ModifyLabels(context.Background(), "token", "msg1", []string{"STARRED"}, []string{"UNREAD"})

	require.NoError(t, err)
}

// --- GetUnreadCount ---

func TestAdapter_GetUnreadCount_withLabel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/labels/INBOX", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gmailapi.Label{Id: "INBOX", MessagesUnread: 7})
	})

	adapter := newTestAdapter(t, mux)
	count, err := adapter.GetUnreadCount(context.Background(), "token", "INBOX")

	require.NoError(t, err)
	assert.Equal(t, int32(7), count)
}

func TestAdapter_GetUnreadCount_allLabels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "is:unread", r.URL.Query().Get("q"))
		json.NewEncoder(w).Encode(gmailapi.ListMessagesResponse{
			ResultSizeEstimate: 42,
		})
	})

	adapter := newTestAdapter(t, mux)
	count, err := adapter.GetUnreadCount(context.Background(), "token", "")

	require.NoError(t, err)
	assert.Equal(t, int32(42), count)
}

// --- GetLabels ---

func TestAdapter_GetLabels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gmailapi.ListLabelsResponse{
			Labels: []*gmailapi.Label{
				{Id: "INBOX", Name: "INBOX", Type: "system"},
				{Id: "Label_1", Name: "work", Type: "user"},
			},
		})
	})

	adapter := newTestAdapter(t, mux)
	labels, err := adapter.GetLabels(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, labels, 2)
	assert.Equal(t, "INBOX", labels[0].Id)
	assert.Equal(t, emailv1.LabelType_SYSTEM, labels[0].Type)
	assert.Equal(t, "Label_1", labels[1].Id)
	assert.Equal(t, "work", labels[1].Name)
	assert.Equal(t, emailv1.LabelType_USER, labels[1].Type)
}

// --- SearchEmails ---

func TestAdapter_SearchEmails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "from:alice subject:invoice", r.URL.Query().Get("q"))
		json.NewEncoder(w).Encode(gmailapi.ListMessagesResponse{
			Messages: []*gmailapi.Message{{Id: "msg42"}},
		})
	})
	mux.HandleFunc("/gmail/v1/users/me/messages/msg42", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(gmailapi.Message{Id: "msg42"})
	})

	adapter := newTestAdapter(t, mux)
	emails, page, err := adapter.SearchEmails(context.Background(), "token", "from:alice subject:invoice", emailv1.EmailFormat_METADATA, 5, "")

	require.NoError(t, err)
	require.Len(t, emails, 1)
	assert.Equal(t, "msg42", emails[0].Id)
	assert.Equal(t, int32(1), page.ResultCount)
}

// --- GetEmail ---

func TestAdapter_GetEmail_metadata(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/msg1", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "metadata", r.URL.Query().Get("format"))
		json.NewEncoder(w).Encode(gmailapi.Message{
			Id: "msg1",
			Payload: &gmailapi.MessagePart{
				Headers: []*gmailapi.MessagePartHeader{
					{Name: "Subject", Value: "Metadata Only"},
				},
				// No Body.Data — metadata format omits body
			},
		})
	})

	adapter := newTestAdapter(t, mux)
	email, err := adapter.GetEmail(context.Background(), "token", "msg1", emailv1.EmailFormat_METADATA)

	require.NoError(t, err)
	assert.Equal(t, "Metadata Only", email.Subject)
	assert.Empty(t, email.Body)
}

func TestAdapter_GetEmail_notFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/missing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write(gmailErrBody(404, "Message not found"))
	})

	adapter := newTestAdapter(t, mux)
	_, err := adapter.GetEmail(context.Background(), "token", "missing", emailv1.EmailFormat_FULL)

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestAdapter_GetEmail_unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/msg1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write(gmailErrBody(401, "Invalid Credentials"))
	})

	adapter := newTestAdapter(t, mux)
	_, err := adapter.GetEmail(context.Background(), "expired-token", "msg1", emailv1.EmailFormat_FULL)

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAdapter_GetEmail_full(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/msg1", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "full", r.URL.Query().Get("format"))
		json.NewEncoder(w).Encode(gmailapi.Message{
			Id:       "msg1",
			ThreadId: "thread1",
			Snippet:  "Hello",
			LabelIds: []string{"INBOX", "UNREAD"},
			Payload: &gmailapi.MessagePart{
				MimeType: "text/plain",
				Headers: []*gmailapi.MessagePartHeader{
					{Name: "Subject", Value: "Test Subject"},
					{Name: "From", Value: "Alice <alice@example.com>"},
					{Name: "To", Value: "Bob <bob@example.com>"},
					{Name: "Date", Value: "Mon, 01 Jan 2024 12:00:00 +0000"},
				},
				Body: &gmailapi.MessagePartBody{Data: "SGVsbG8gd29ybGQ"},
			},
		})
	})

	adapter := newTestAdapter(t, mux)
	email, err := adapter.GetEmail(context.Background(), "token", "msg1", emailv1.EmailFormat_FULL)

	require.NoError(t, err)
	assert.Equal(t, "msg1", email.Id)
	assert.Equal(t, "thread1", email.ThreadId)
	assert.Equal(t, "Test Subject", email.Subject)
	assert.Equal(t, "Hello", email.Snippet)
	assert.Equal(t, emailv1.Provider_GMAIL, email.Provider)
	assert.Equal(t, []string{"INBOX", "UNREAD"}, email.LabelIds)
	require.NotNil(t, email.From)
	assert.Equal(t, "Alice", email.From.Name)
	assert.Equal(t, "alice@example.com", email.From.Email)
	require.Len(t, email.To, 1)
	assert.Equal(t, "bob@example.com", email.To[0].Email)
	assert.Equal(t, "Hello world", email.Body)
	assert.NotNil(t, email.Date)
}
