package fanout_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
	"github.com/alaexeyshustov/email-fetcher/internal/fanout"
	"github.com/alaexeyshustov/email-fetcher/internal/provider"
)

// fakeProvider implements provider.Provider for fanout unit tests.
type fakeProvider struct {
	name           string
	listEmailsFn   func(context.Context, string, emailv1.EmailFormat, int32, string, []string) ([]*emailv1.Email, *emailv1.PageInfo, error)
	searchEmailsFn func(context.Context, string, string, emailv1.EmailFormat, int32, string) ([]*emailv1.Email, *emailv1.PageInfo, error)
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
func (f *fakeProvider) GetEmail(context.Context, string, string, emailv1.EmailFormat) (*emailv1.Email, error) {
	return nil, nil
}
func (f *fakeProvider) GetLabels(context.Context, string) ([]*emailv1.Label, error) { return nil, nil }
func (f *fakeProvider) GetUnreadCount(context.Context, string, string) (int32, error) { return 0, nil }
func (f *fakeProvider) ModifyLabels(context.Context, string, string, []string, []string) error {
	return nil
}
func (f *fakeProvider) GetAttachmentContent(context.Context, string, string, string) (*emailv1.AttachmentContent, error) {
	return nil, nil
}

var _ provider.Provider = (*fakeProvider)(nil)

// drainResults collects all Results from the channel into a slice.
func drainResults(ch <-chan fanout.Result) []fanout.Result {
	var out []fanout.Result
	for r := range ch {
		out = append(out, r)
	}
	return out
}

// --- BuildCompositeToken ---

func TestBuildCompositeToken_empty(t *testing.T) {
	assert.Empty(t, fanout.BuildCompositeToken(nil))
	assert.Empty(t, fanout.BuildCompositeToken(map[string]string{}))
}

func TestBuildCompositeToken_nonEmpty(t *testing.T) {
	tok := fanout.BuildCompositeToken(map[string]string{"gmail": "g1"})
	assert.NotEmpty(t, tok)
}

// --- Composite cursor passthrough ---

func TestListEmails_compositeCursorPassthrough(t *testing.T) {
	var gmailGot, yahooGot string

	gmail := &fakeProvider{
		name: "gmail",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, pageToken string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			gmailGot = pageToken
			return nil, &emailv1.PageInfo{}, nil
		},
	}
	yahoo := &fakeProvider{
		name: "yahoo",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, pageToken string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			yahooGot = pageToken
			return nil, &emailv1.PageInfo{}, nil
		},
	}

	composite := fanout.BuildCompositeToken(map[string]string{"gmail": "g-page2", "yahoo": "y-page2"})

	f := fanout.New(map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: gmail,
		emailv1.Provider_YAHOO: yahoo,
	})
	ch := f.ListEmails(context.Background(), []*emailv1.ProviderCredentials{
		{Provider: emailv1.Provider_GMAIL, AccessToken: "gtok"},
		{Provider: emailv1.Provider_YAHOO, AccessToken: "ytok"},
	}, emailv1.EmailFormat_METADATA, 10, composite, nil)
	drainResults(ch)

	assert.Equal(t, "g-page2", gmailGot)
	assert.Equal(t, "y-page2", yahooGot)
}

// --- Merge ---

func TestListEmails_mergesAllProviders(t *testing.T) {
	gmail := &fakeProvider{
		name: "gmail",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, _ string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			return []*emailv1.Email{{Id: "gmail/1"}, {Id: "gmail/2"}}, &emailv1.PageInfo{ResultCount: 2}, nil
		},
	}
	yahoo := &fakeProvider{
		name: "yahoo",
		listEmailsFn: func(_ context.Context, _ string, _ emailv1.EmailFormat, _ int32, _ string, _ []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			return []*emailv1.Email{{Id: "INBOX/10"}}, &emailv1.PageInfo{ResultCount: 1}, nil
		},
	}

	f := fanout.New(map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: gmail,
		emailv1.Provider_YAHOO: yahoo,
	})
	results := drainResults(f.ListEmails(context.Background(), []*emailv1.ProviderCredentials{
		{Provider: emailv1.Provider_GMAIL, AccessToken: "gtok"},
		{Provider: emailv1.Provider_YAHOO, AccessToken: "ytok"},
	}, emailv1.EmailFormat_METADATA, 0, "", nil))

	require.Len(t, results, 2)
	var total int
	for _, r := range results {
		require.NoError(t, r.Err)
		total += len(r.Emails)
	}
	assert.Equal(t, 3, total)
}

// --- Partial failure ---

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
			return nil, nil, errors.New("connection refused")
		},
	}

	f := fanout.New(map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: gmail,
		emailv1.Provider_YAHOO: yahoo,
	})
	results := drainResults(f.ListEmails(context.Background(), []*emailv1.ProviderCredentials{
		{Provider: emailv1.Provider_GMAIL, AccessToken: "gtok"},
		{Provider: emailv1.Provider_YAHOO, AccessToken: "ytok"},
	}, emailv1.EmailFormat_METADATA, 0, "", nil))

	require.Len(t, results, 2)
	var successCount, errCount int
	for _, r := range results {
		if r.Err != nil {
			errCount++
			assert.Equal(t, "yahoo", r.ProviderName)
		} else {
			successCount++
			assert.Equal(t, "gmail", r.ProviderName)
		}
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, errCount)
}

// --- Unknown provider credential silently skipped ---

func TestListEmails_unknownProviderSkipped(t *testing.T) {
	f := fanout.New(map[emailv1.Provider]provider.Provider{})
	results := drainResults(f.ListEmails(context.Background(), []*emailv1.ProviderCredentials{
		{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"},
	}, emailv1.EmailFormat_METADATA, 0, "", nil))

	assert.Empty(t, results)
}

// --- SearchEmails composite cursor passthrough ---

func TestSearchEmails_compositeCursorPassthrough(t *testing.T) {
	var gotToken string

	fake := &fakeProvider{
		name: "gmail",
		searchEmailsFn: func(_ context.Context, _ string, _ string, _ emailv1.EmailFormat, _ int32, pageToken string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
			gotToken = pageToken
			return nil, &emailv1.PageInfo{}, nil
		},
	}

	composite := fanout.BuildCompositeToken(map[string]string{"gmail": "search-p2"})

	f := fanout.New(map[emailv1.Provider]provider.Provider{
		emailv1.Provider_GMAIL: fake,
	})
	drainResults(f.SearchEmails(context.Background(), []*emailv1.ProviderCredentials{
		{Provider: emailv1.Provider_GMAIL, AccessToken: "tok"},
	}, "invoice", emailv1.EmailFormat_METADATA, 10, composite))

	assert.Equal(t, "search-p2", gotToken)
}
