package yahoo

import (
	"crypto/tls"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-sasl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const imapAddr = "imap.mail.yahoo.com:993"

// IMAPConn abstracts the go-imap client operations used by the adapter.
// In production, *client.Client satisfies this interface.
// In tests, a fake struct satisfies it.
type IMAPConn interface {
	Select(name string, readOnly bool) (*imap.MailboxStatus, error)
	List(ref, name string, ch chan *imap.MailboxInfo) error
	Status(name string, items []imap.StatusItem) (*imap.MailboxStatus, error)
	UidSearch(criteria *imap.SearchCriteria) ([]uint32, error)
	UidFetch(seqset *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error
	UidCopy(seqset *imap.SeqSet, dest string) error
	UidStore(seqset *imap.SeqSet, item imap.StoreItem, value interface{}, ch chan *imap.Message) error
	Expunge(ch chan uint32) error
	Logout() error
}

// ConnectFunc creates an authenticated IMAP connection for the given token.
// Overridden in tests via WithConnectFunc.
type ConnectFunc func(token string) (IMAPConn, error)

// defaultConnect dials Yahoo's IMAP server and authenticates with OAUTHBEARER.
// OAUTHBEARER (RFC 7628) is used over XOAUTH2 because it does not require
// the user's email address in the SASL exchange — the token identifies the user.
func defaultConnect(token string) (IMAPConn, error) {
	c, err := client.DialTLS(imapAddr, &tls.Config{MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "yahoo: failed to connect: %v", err)
	}

	auth := sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{Token: token})
	if err := c.Authenticate(auth); err != nil {
		_ = c.Logout()
		return nil, status.Errorf(codes.Unauthenticated, "yahoo: authentication failed: %v", err)
	}

	return c, nil
}
