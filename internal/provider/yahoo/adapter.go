package yahoo

import (
	"context"
	"sort"
	"strconv"

	"github.com/emersion/go-imap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
)

// Adapter implements provider.Provider for Yahoo Mail using IMAP.
type Adapter struct {
	connect ConnectFunc
}

// Option configures the Adapter.
type Option func(*Adapter)

// WithConnectFunc overrides the IMAP connection factory. Used in tests.
func WithConnectFunc(fn ConnectFunc) Option {
	return func(a *Adapter) { a.connect = fn }
}

func New(opts ...Option) *Adapter {
	a := &Adapter{connect: defaultConnect}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *Adapter) Name() string { return "yahoo" }

// withConn acquires a connection, runs fn, and always logs out.
func (a *Adapter) withConn(_ context.Context, token string, fn func(IMAPConn) error) error {
	conn, err := a.connect(token)
	if err != nil {
		return err
	}
	defer conn.Logout() //nolint:errcheck
	return fn(conn)
}

func (a *Adapter) ListEmails(ctx context.Context, token string, format emailv1.EmailFormat, maxResults int32, pageToken string, labelIDs []string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
	mailbox := resolveMailbox(labelIDs)
	var emails []*emailv1.Email
	var nextToken string

	err := a.withConn(ctx, token, func(conn IMAPConn) error {
		if _, err := conn.Select(mailbox, true); err != nil {
			return mapError(err)
		}

		criteria := &imap.SearchCriteria{}
		if pageToken != "" {
			minUID, err := strconv.ParseUint(pageToken, 10, 32)
			if err == nil && minUID > 0 {
				set := new(imap.SeqSet)
				set.AddRange(1, uint32(minUID)-1)
				criteria.Uid = set
			}
		}

		uids, err := conn.UidSearch(criteria)
		if err != nil {
			return mapError(err)
		}

		// Most-recent-first (UIDs are ascending).
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })

		if maxResults > 0 && int32(len(uids)) > maxResults {
			// The oldest UID on this page becomes the cursor for the next page.
			nextToken = strconv.FormatUint(uint64(uids[maxResults-1]), 10)
			uids = uids[:maxResults]
		}

		if len(uids) == 0 {
			return nil
		}

		msgs, err := fetchByUIDs(conn, uids, format)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			emails = append(emails, toEmail(m, mailbox))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return emails, &emailv1.PageInfo{NextPageToken: nextToken, ResultCount: int32(len(emails))}, nil
}

func (a *Adapter) SearchEmails(ctx context.Context, token string, query string, format emailv1.EmailFormat, maxResults int32, pageToken string) ([]*emailv1.Email, *emailv1.PageInfo, error) {
	const mailbox = "INBOX"
	var emails []*emailv1.Email
	var nextToken string

	err := a.withConn(ctx, token, func(conn IMAPConn) error {
		if _, err := conn.Select(mailbox, true); err != nil {
			return mapError(err)
		}

		criteria := &imap.SearchCriteria{Text: []string{query}}
		if pageToken != "" {
			minUID, err := strconv.ParseUint(pageToken, 10, 32)
			if err == nil && minUID > 0 {
				set := new(imap.SeqSet)
				set.AddRange(1, uint32(minUID)-1)
				criteria.Uid = set
			}
		}

		uids, err := conn.UidSearch(criteria)
		if err != nil {
			return mapError(err)
		}

		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })

		if maxResults > 0 && int32(len(uids)) > maxResults {
			nextToken = strconv.FormatUint(uint64(uids[maxResults-1]), 10)
			uids = uids[:maxResults]
		}

		if len(uids) == 0 {
			return nil
		}

		msgs, err := fetchByUIDs(conn, uids, format)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			emails = append(emails, toEmail(m, mailbox))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return emails, &emailv1.PageInfo{NextPageToken: nextToken, ResultCount: int32(len(emails))}, nil
}

func (a *Adapter) GetEmail(ctx context.Context, token string, id string, format emailv1.EmailFormat) (*emailv1.Email, error) {
	mailbox, uid, err := parseCompoundID(id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	var email *emailv1.Email
	err = a.withConn(ctx, token, func(conn IMAPConn) error {
		if _, err := conn.Select(mailbox, true); err != nil {
			return mapError(err)
		}

		seqset := new(imap.SeqSet)
		seqset.AddNum(uid)

		msgs, err := fetchByUIDs(conn, []uint32{uid}, format)
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return status.Errorf(codes.NotFound, "yahoo: message %s not found", id)
		}
		email = toEmail(msgs[0], mailbox)
		return nil
	})
	return email, err
}

func (a *Adapter) GetLabels(ctx context.Context, token string) ([]*emailv1.Label, error) {
	var labels []*emailv1.Label
	err := a.withConn(ctx, token, func(conn IMAPConn) error {
		ch := make(chan *imap.MailboxInfo, 20)
		done := make(chan error, 1)
		go func() { done <- conn.List("", "*", ch) }()

		for info := range ch {
			labels = append(labels, toLabel(info))
		}
		return mapError(<-done)
	})
	return labels, err
}

func (a *Adapter) GetUnreadCount(ctx context.Context, token string, labelID string) (int32, error) {
	mailbox := labelID
	if mailbox == "" {
		mailbox = "INBOX"
	}

	var count int32
	err := a.withConn(ctx, token, func(conn IMAPConn) error {
		s, err := conn.Status(mailbox, []imap.StatusItem{imap.StatusUnseen})
		if err != nil {
			return mapError(err)
		}
		count = int32(s.Unseen)
		return nil
	})
	return count, err
}

func (a *Adapter) ModifyLabels(ctx context.Context, token string, messageID string, addLabelIDs, removeLabelIDs []string) error {
	mailbox, uid, err := parseCompoundID(messageID)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "%v", err)
	}

	return a.withConn(ctx, token, func(conn IMAPConn) error {
		if _, err := conn.Select(mailbox, false); err != nil {
			return mapError(err)
		}

		seqset := new(imap.SeqSet)
		seqset.AddNum(uid)

		// Add labels: COPY to each target mailbox.
		// IMAP is one-to-one folder; "adding a label" means copying to the folder.
		for _, dest := range addLabelIDs {
			if err := conn.UidCopy(seqset, dest); err != nil {
				return mapError(err)
			}
		}

		// Remove labels: if removing source mailbox, mark \Deleted + EXPUNGE.
		// This is COPY+EXPUNGE — not atomic. UID changes after the operation.
		for _, src := range removeLabelIDs {
			if src == mailbox {
				if err := conn.UidStore(seqset, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil); err != nil {
					return mapError(err)
				}
				if err := conn.Expunge(nil); err != nil {
					return mapError(err)
				}
			}
		}

		return nil
	})
}

func (a *Adapter) GetAttachmentContent(ctx context.Context, token string, messageID, attachmentID string) (*emailv1.AttachmentContent, error) {
	mailbox, uid, err := parseCompoundID(messageID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}

	var content *emailv1.AttachmentContent
	err = a.withConn(ctx, token, func(conn IMAPConn) error {
		if _, err := conn.Select(mailbox, true); err != nil {
			return mapError(err)
		}

		// attachmentID is the MIME part number (e.g. "2", "2.1").
		section, err := imap.ParseBodySectionName(imap.FetchItem("BODY[" + attachmentID + "]"))
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "yahoo: invalid attachment id %q: %v", attachmentID, err)
		}

		seqset := new(imap.SeqSet)
		seqset.AddNum(uid)
		items := []imap.FetchItem{section.FetchItem()}

		msgs, err := fetchMessages(conn, seqset, items)
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return status.Errorf(codes.NotFound, "yahoo: message %s not found", messageID)
		}

		r := msgs[0].GetBody(section)
		if r == nil {
			return status.Errorf(codes.NotFound, "yahoo: attachment %s not found in message %s", attachmentID, messageID)
		}

		data, err := readAll(r)
		if err != nil {
			return status.Errorf(codes.Internal, "yahoo: failed to read attachment: %v", err)
		}
		content = &emailv1.AttachmentContent{Content: data}
		return nil
	})
	return content, err
}

// ── helpers ──────────────────────────────────────────────────────────────────

func resolveMailbox(labelIDs []string) string {
	if len(labelIDs) > 0 && labelIDs[0] != "" {
		return labelIDs[0]
	}
	return "INBOX"
}

func fetchItemsForFormat(format emailv1.EmailFormat) []imap.FetchItem {
	base := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags}
	if format == emailv1.EmailFormat_FULL {
		return append(base, textBodySection().FetchItem())
	}
	return base
}

func fetchByUIDs(conn IMAPConn, uids []uint32, format emailv1.EmailFormat) ([]*imap.Message, error) {
	seqset := new(imap.SeqSet)
	for _, uid := range uids {
		seqset.AddNum(uid)
	}
	return fetchMessages(conn, seqset, fetchItemsForFormat(format))
}

func fetchMessages(conn IMAPConn, seqset *imap.SeqSet, items []imap.FetchItem) ([]*imap.Message, error) {
	ch := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func() { done <- conn.UidFetch(seqset, items, ch) }()

	var msgs []*imap.Message
	for m := range ch {
		msgs = append(msgs, m)
	}
	if err := <-done; err != nil {
		return nil, mapError(err)
	}
	return msgs, nil
}

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return buf, err
		}
	}
	return buf, nil
}
