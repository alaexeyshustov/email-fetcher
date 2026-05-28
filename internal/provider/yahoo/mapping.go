package yahoo

import (
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-imap"
	"google.golang.org/protobuf/types/known/timestamppb"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
)

// parseCompoundID splits "INBOX/42" into mailbox="INBOX" and uid=42.
func parseCompoundID(id string) (mailbox string, uid uint32, err error) {
	idx := strings.LastIndex(id, "/")
	if idx < 0 {
		return "", 0, fmt.Errorf("invalid yahoo message id %q: expected mailbox/uid", id)
	}
	mailbox = id[:idx]
	var n uint32
	if _, err = fmt.Sscanf(id[idx+1:], "%d", &n); err != nil {
		return "", 0, fmt.Errorf("invalid yahoo message id %q: uid not numeric", id)
	}
	return mailbox, n, nil
}

func toEmail(msg *imap.Message, mailbox string) *emailv1.Email {
	e := &emailv1.Email{
		Id:       fmt.Sprintf("%s/%d", mailbox, msg.Uid),
		Provider: emailv1.Provider_YAHOO,
		LabelIds: []string{mailbox},
	}

	if msg.Envelope != nil {
		env := msg.Envelope
		e.Subject = env.Subject
		e.ThreadId = synthesizeThreadID(env)
		if !env.Date.IsZero() {
			e.Date = timestamppb.New(env.Date)
		}
		if len(env.From) > 0 {
			e.From = imapAddrToProto(env.From[0])
		}
		e.To = imapAddrsToProto(env.To)
	}

	// Map IMAP \Flagged to STARRED label
	for _, flag := range msg.Flags {
		if flag == imap.FlaggedFlag {
			e.LabelIds = append(e.LabelIds, "STARRED")
		}
	}

	// Extract body from BODY[TEXT] section if present
	section := textBodySection()
	if r := msg.GetBody(section); r != nil {
		if data, err := io.ReadAll(r); err == nil {
			e.Body = string(data)
		}
	}

	return e
}

// textBodySection returns the BODY[TEXT] section used for full-format fetches.
func textBodySection() *imap.BodySectionName {
	return &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{Specifier: imap.TextSpecifier},
	}
}

// synthesizeThreadID infers a thread identifier from RFC 2822 headers.
// Uses In-Reply-To when present (best-effort, not guaranteed stable).
func synthesizeThreadID(env *imap.Envelope) string {
	if env.InReplyTo != "" {
		// In-Reply-To may contain multiple IDs; take the first.
		return strings.Fields(env.InReplyTo)[0]
	}
	return env.MessageId
}

func toLabel(info *imap.MailboxInfo) *emailv1.Label {
	lt := emailv1.LabelType_USER
	for _, attr := range info.Attributes {
		if isSystemAttribute(attr) {
			lt = emailv1.LabelType_SYSTEM
			break
		}
	}
	return &emailv1.Label{
		Id:   info.Name,
		Name: info.Name,
		Type: lt,
	}
}

// isSystemAttribute returns true for IMAP RFC 6154 special-use attributes.
func isSystemAttribute(attr string) bool {
	switch strings.ToLower(attr) {
	case `\inbox`, `\sent`, `\drafts`, `\trash`, `\junk`, `\flagged`, `\all`, `\archive`:
		return true
	}
	return false
}

func imapAddrToProto(addr *imap.Address) *emailv1.Address {
	if addr == nil {
		return nil
	}
	email := addr.MailboxName
	if addr.HostName != "" {
		email += "@" + addr.HostName
	}
	return &emailv1.Address{Name: addr.PersonalName, Email: email}
}

func imapAddrsToProto(addrs []*imap.Address) []*emailv1.Address {
	out := make([]*emailv1.Address, 0, len(addrs))
	for _, a := range addrs {
		if p := imapAddrToProto(a); p != nil {
			out = append(out, p)
		}
	}
	return out
}
