package yahoo_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
	"github.com/alaexeyshustov/email-fetcher/internal/provider/yahoo"
)

// fakeIMAPConn implements yahoo.IMAPConn for testing.
// Nil function fields return zero values without error.
type fakeIMAPConn struct {
	selectFn    func(name string, readOnly bool) (*imap.MailboxStatus, error)
	listFn      func(ref, name string, ch chan *imap.MailboxInfo) error
	statusFn    func(name string, items []imap.StatusItem) (*imap.MailboxStatus, error)
	uidSearchFn func(criteria *imap.SearchCriteria) ([]uint32, error)
	uidFetchFn  func(seqset *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error
	uidCopyFn   func(seqset *imap.SeqSet, dest string) error
	uidStoreFn  func(seqset *imap.SeqSet, item imap.StoreItem, value interface{}, ch chan *imap.Message) error
	expungeFn   func(ch chan uint32) error
}

func (f *fakeIMAPConn) Select(name string, readOnly bool) (*imap.MailboxStatus, error) {
	if f.selectFn != nil {
		return f.selectFn(name, readOnly)
	}
	return &imap.MailboxStatus{Name: name}, nil
}
func (f *fakeIMAPConn) List(ref, name string, ch chan *imap.MailboxInfo) error {
	if f.listFn != nil {
		return f.listFn(ref, name, ch)
	}
	close(ch)
	return nil
}
func (f *fakeIMAPConn) Status(name string, items []imap.StatusItem) (*imap.MailboxStatus, error) {
	if f.statusFn != nil {
		return f.statusFn(name, items)
	}
	return &imap.MailboxStatus{Name: name}, nil
}
func (f *fakeIMAPConn) UidSearch(criteria *imap.SearchCriteria) ([]uint32, error) {
	if f.uidSearchFn != nil {
		return f.uidSearchFn(criteria)
	}
	return nil, nil
}
func (f *fakeIMAPConn) UidFetch(seqset *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error {
	if f.uidFetchFn != nil {
		return f.uidFetchFn(seqset, items, ch)
	}
	close(ch)
	return nil
}
func (f *fakeIMAPConn) UidCopy(seqset *imap.SeqSet, dest string) error {
	if f.uidCopyFn != nil {
		return f.uidCopyFn(seqset, dest)
	}
	return nil
}
func (f *fakeIMAPConn) UidStore(seqset *imap.SeqSet, item imap.StoreItem, value interface{}, ch chan *imap.Message) error {
	if f.uidStoreFn != nil {
		return f.uidStoreFn(seqset, item, value, ch)
	}
	return nil
}
func (f *fakeIMAPConn) Expunge(ch chan uint32) error {
	if f.expungeFn != nil {
		return f.expungeFn(ch)
	}
	return nil
}
func (f *fakeIMAPConn) Logout() error { return nil }

// newTestAdapter wires an adapter to use the given fake connection.
func newTestAdapter(t *testing.T, conn yahoo.IMAPConn) *yahoo.Adapter {
	t.Helper()
	return yahoo.New(yahoo.WithConnectFunc(func(token string) (yahoo.IMAPConn, error) {
		return conn, nil
	}))
}

// sendMessages is a helper that writes messages to the UidFetch channel and closes it.
func sendMessages(msgs ...*imap.Message) func(*imap.SeqSet, []imap.FetchItem, chan *imap.Message) error {
	return func(_ *imap.SeqSet, _ []imap.FetchItem, ch chan *imap.Message) error {
		for _, m := range msgs {
			ch <- m
		}
		close(ch)
		return nil
	}
}

// --- ListEmails ---

func TestAdapter_ListEmails(t *testing.T) {
	fake := &fakeIMAPConn{
		uidSearchFn: func(_ *imap.SearchCriteria) ([]uint32, error) {
			return []uint32{10, 20, 30}, nil // ascending UIDs
		},
		uidFetchFn: sendMessages(
			&imap.Message{Uid: 30, Envelope: &imap.Envelope{Subject: "Newest"}},
			&imap.Message{Uid: 20, Envelope: &imap.Envelope{Subject: "Middle"}},
			&imap.Message{Uid: 10, Envelope: &imap.Envelope{Subject: "Oldest"}},
		),
	}
	adapter := newTestAdapter(t, fake)
	emails, page, err := adapter.ListEmails(context.Background(), "token", emailv1.EmailFormat_METADATA, 0, "", nil)

	require.NoError(t, err)
	assert.Len(t, emails, 3)
	assert.Equal(t, int32(3), page.ResultCount)
	assert.Empty(t, page.NextPageToken)
	for _, e := range emails {
		assert.Equal(t, emailv1.Provider_YAHOO, e.Provider)
	}
}

func TestAdapter_ListEmails_pagination(t *testing.T) {
	// First page: maxResults=2, returns UIDs 30 and 20; cursor = "20"
	var searchCriteriaUsed *imap.SearchCriteria
	fake := &fakeIMAPConn{
		uidSearchFn: func(c *imap.SearchCriteria) ([]uint32, error) {
			searchCriteriaUsed = c
			return []uint32{10, 20, 30}, nil
		},
		uidFetchFn: sendMessages(
			&imap.Message{Uid: 30, Envelope: &imap.Envelope{Subject: "A"}},
			&imap.Message{Uid: 20, Envelope: &imap.Envelope{Subject: "B"}},
		),
	}
	adapter := newTestAdapter(t, fake)
	emails, page, err := adapter.ListEmails(context.Background(), "token", emailv1.EmailFormat_METADATA, 2, "", nil)

	require.NoError(t, err)
	assert.Len(t, emails, 2)
	assert.Equal(t, "20", page.NextPageToken)
	_ = searchCriteriaUsed // criteria was ALL on first page
}

// --- ModifyLabels ---

func TestAdapter_ModifyLabels_copyAndExpunge(t *testing.T) {
	var copiedTo string
	var storedFlags interface{}
	expungeCalled := false

	fake := &fakeIMAPConn{
		uidCopyFn: func(seqset *imap.SeqSet, dest string) error {
			copiedTo = dest
			return nil
		},
		uidStoreFn: func(seqset *imap.SeqSet, item imap.StoreItem, value interface{}, ch chan *imap.Message) error {
			storedFlags = value
			return nil
		},
		expungeFn: func(ch chan uint32) error {
			expungeCalled = true
			return nil
		},
	}

	adapter := newTestAdapter(t, fake)
	// Move message from INBOX to Archive: add=["Archive"], remove=["INBOX"]
	err := adapter.ModifyLabels(context.Background(), "token", "INBOX/42", []string{"Archive"}, []string{"INBOX"})

	require.NoError(t, err)
	assert.Equal(t, "Archive", copiedTo)
	assert.Equal(t, []interface{}{imap.DeletedFlag}, storedFlags)
	assert.True(t, expungeCalled, "EXPUNGE must be called to complete the move")
}

func TestAdapter_ModifyLabels_addOnly(t *testing.T) {
	copyCalled := false
	expungeCalled := false

	fake := &fakeIMAPConn{
		uidCopyFn: func(_ *imap.SeqSet, _ string) error {
			copyCalled = true
			return nil
		},
		expungeFn: func(_ chan uint32) error {
			expungeCalled = true
			return nil
		},
	}

	adapter := newTestAdapter(t, fake)
	// Add STARRED label without removing source — no expunge
	err := adapter.ModifyLabels(context.Background(), "token", "INBOX/42", []string{"STARRED"}, nil)

	require.NoError(t, err)
	assert.True(t, copyCalled)
	assert.False(t, expungeCalled, "EXPUNGE must NOT be called when source mailbox is not removed")
}

// --- GetLabels ---

func TestAdapter_GetLabels(t *testing.T) {
	fake := &fakeIMAPConn{
		listFn: func(ref, name string, ch chan *imap.MailboxInfo) error {
			ch <- &imap.MailboxInfo{Name: "INBOX", Attributes: []string{`\Inbox`}}
			ch <- &imap.MailboxInfo{Name: "Projects", Attributes: []string{`\HasNoChildren`}}
			close(ch)
			return nil
		},
	}
	adapter := newTestAdapter(t, fake)
	labels, err := adapter.GetLabels(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, labels, 2)
	assert.Equal(t, "INBOX", labels[0].Id)
	assert.Equal(t, emailv1.LabelType_SYSTEM, labels[0].Type)
	assert.Equal(t, "Projects", labels[1].Id)
	assert.Equal(t, emailv1.LabelType_USER, labels[1].Type)
}

// --- GetUnreadCount ---

func TestAdapter_GetUnreadCount_withLabel(t *testing.T) {
	fake := &fakeIMAPConn{
		statusFn: func(name string, items []imap.StatusItem) (*imap.MailboxStatus, error) {
			assert.Equal(t, "INBOX", name)
			return &imap.MailboxStatus{Name: "INBOX", Unseen: 7}, nil
		},
	}
	adapter := newTestAdapter(t, fake)
	count, err := adapter.GetUnreadCount(context.Background(), "token", "INBOX")

	require.NoError(t, err)
	assert.Equal(t, int32(7), count)
}

func TestAdapter_GetUnreadCount_allLabels(t *testing.T) {
	fake := &fakeIMAPConn{
		statusFn: func(name string, items []imap.StatusItem) (*imap.MailboxStatus, error) {
			assert.Equal(t, "INBOX", name) // defaults to INBOX when no label given
			return &imap.MailboxStatus{Name: "INBOX", Unseen: 42}, nil
		},
	}
	adapter := newTestAdapter(t, fake)
	count, err := adapter.GetUnreadCount(context.Background(), "token", "")

	require.NoError(t, err)
	assert.Equal(t, int32(42), count)
}

// --- SearchEmails ---

func TestAdapter_SearchEmails(t *testing.T) {
	var gotCriteria *imap.SearchCriteria

	fake := &fakeIMAPConn{
		uidSearchFn: func(c *imap.SearchCriteria) ([]uint32, error) {
			gotCriteria = c
			return []uint32{55}, nil
		},
		uidFetchFn: sendMessages(
			&imap.Message{Uid: 55, Envelope: &imap.Envelope{Subject: "Invoice Jan"}},
		),
	}
	adapter := newTestAdapter(t, fake)
	emails, page, err := adapter.SearchEmails(context.Background(), "token", "invoice", emailv1.EmailFormat_METADATA, 5, "")

	require.NoError(t, err)
	require.Len(t, emails, 1)
	assert.Equal(t, "INBOX/55", emails[0].Id)
	assert.Equal(t, int32(1), page.ResultCount)
	require.NotNil(t, gotCriteria)
	assert.Equal(t, []string{"invoice"}, gotCriteria.Text)
}

// --- GetEmail ---

func TestAdapter_GetEmail_notFound(t *testing.T) {
	fake := &fakeIMAPConn{
		uidFetchFn: sendMessages(), // no messages returned
	}
	adapter := newTestAdapter(t, fake)
	_, err := adapter.GetEmail(context.Background(), "token", "INBOX/99", emailv1.EmailFormat_FULL)

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestAdapter_GetEmail_unauthenticated(t *testing.T) {
	fake := &fakeIMAPConn{
		selectFn: func(name string, readOnly bool) (*imap.MailboxStatus, error) {
			return nil, errors.New("NO [AUTHENTICATIONFAILED] Invalid credentials (Failure)")
		},
	}
	adapter := newTestAdapter(t, fake)
	_, err := adapter.GetEmail(context.Background(), "expired-token", "INBOX/1", emailv1.EmailFormat_FULL)

	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAdapter_GetEmail_full(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	uid := uint32(42)

	fake := &fakeIMAPConn{
		uidFetchFn: sendMessages(&imap.Message{
			Uid: uid,
			Envelope: &imap.Envelope{
				Subject:   "Test Subject",
				Date:      now,
				From:      []*imap.Address{{PersonalName: "Alice", MailboxName: "alice", HostName: "example.com"}},
				To:        []*imap.Address{{PersonalName: "Bob", MailboxName: "bob", HostName: "example.com"}},
				MessageId: "<msg42@example.com>",
			},
			Flags: []string{imap.SeenFlag},
		}),
	}

	adapter := newTestAdapter(t, fake)
	email, err := adapter.GetEmail(context.Background(), "token", fmt.Sprintf("INBOX/%d", uid), emailv1.EmailFormat_FULL)

	require.NoError(t, err)
	assert.Equal(t, "INBOX/42", email.Id)
	assert.Equal(t, "Test Subject", email.Subject)
	assert.Equal(t, emailv1.Provider_YAHOO, email.Provider)
	assert.Equal(t, "<msg42@example.com>", email.ThreadId)
	require.NotNil(t, email.From)
	assert.Equal(t, "Alice", email.From.Name)
	assert.Equal(t, "alice@example.com", email.From.Email)
	require.Len(t, email.To, 1)
	assert.Equal(t, "bob@example.com", email.To[0].Email)
	assert.NotNil(t, email.Date)
}

// bytesLiteral satisfies imap.Literal (io.Reader + Len()).
type bytesLiteral struct{ *bytes.Reader }

func (b bytesLiteral) Len() int { return b.Reader.Len() }

// --- GetAttachmentContent ---

func TestAdapter_GetAttachmentContent(t *testing.T) {
	attachmentData := []byte("PDF content here")

	fake := &fakeIMAPConn{
		uidFetchFn: func(_ *imap.SeqSet, _ []imap.FetchItem, ch chan *imap.Message) error {
			msg := imap.NewMessage(0, []imap.FetchItem{"BODY[2]"})
			section, _ := imap.ParseBodySectionName("BODY[2]")
			msg.Body[section] = bytesLiteral{bytes.NewReader(attachmentData)}
			ch <- msg
			close(ch)
			return nil
		},
	}
	adapter := newTestAdapter(t, fake)
	content, err := adapter.GetAttachmentContent(context.Background(), "token", "INBOX/42", "2")

	require.NoError(t, err)
	require.NotNil(t, content)
	assert.Equal(t, attachmentData, content.Content)
}
