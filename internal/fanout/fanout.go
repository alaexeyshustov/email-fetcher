package fanout

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
	"github.com/alaexeyshustov/email-fetcher/internal/provider"
)

// Fanout fans requests out to multiple provider.Provider instances concurrently,
// merges results, and surfaces partial failures in gRPC trailing metadata
// (x-failed-providers, x-provider-errors) rather than aborting the stream.
type Fanout struct {
	providers map[emailv1.Provider]provider.Provider
}

func New(providers map[emailv1.Provider]provider.Provider) *Fanout {
	return &Fanout{providers: providers}
}

// Lookup returns the provider for the given enum value, or InvalidArgument if unknown.
func (f *Fanout) Lookup(p emailv1.Provider) (provider.Provider, error) {
	prov, ok := f.providers[p]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unknown provider: %v", p)
	}
	return prov, nil
}

// Result carries the outcome of a single provider's fan-out call.
type Result struct {
	ProviderName  string
	Emails        []*emailv1.Email
	NextPageToken string
	Err           error
}

// ListEmails dispatches ListEmails concurrently to every provider in creds and
// returns a channel that emits one Result per provider, then closes.
func (f *Fanout) ListEmails(ctx context.Context, creds []*emailv1.ProviderCredentials, format emailv1.EmailFormat, maxResults int32, pageToken string, labelIDs []string) <-chan Result {
	cur := parseCursor(pageToken)
	ch := make(chan Result, len(creds))
	var wg sync.WaitGroup

	for _, cred := range creds {
		prov, ok := f.providers[cred.GetProvider()]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(p provider.Provider, token, provToken string) {
			defer wg.Done()
			emails, page, err := p.ListEmails(ctx, token, format, maxResults, provToken, labelIDs)
			r := Result{ProviderName: p.Name(), Emails: emails, Err: err}
			if page != nil {
				r.NextPageToken = page.NextPageToken
			}
			ch <- r
		}(prov, cred.GetAccessToken(), cur.get(prov.Name()))
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	return ch
}

// SearchEmails dispatches SearchEmails concurrently to every provider in creds
// and returns a channel that emits one Result per provider, then closes.
func (f *Fanout) SearchEmails(ctx context.Context, creds []*emailv1.ProviderCredentials, query string, format emailv1.EmailFormat, maxResults int32, pageToken string) <-chan Result {
	cur := parseCursor(pageToken)
	ch := make(chan Result, len(creds))
	var wg sync.WaitGroup

	for _, cred := range creds {
		prov, ok := f.providers[cred.GetProvider()]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(p provider.Provider, token, provToken string) {
			defer wg.Done()
			emails, page, err := p.SearchEmails(ctx, token, query, format, maxResults, provToken)
			r := Result{ProviderName: p.Name(), Emails: emails, Err: err}
			if page != nil {
				r.NextPageToken = page.NextPageToken
			}
			ch <- r
		}(prov, cred.GetAccessToken(), cur.get(prov.Name()))
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	return ch
}
