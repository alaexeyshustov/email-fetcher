package gmail

import (
	"encoding/base64"
	"net/mail"
	"strings"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
)

func toEmail(msg *gmailapi.Message) *emailv1.Email {
	e := &emailv1.Email{
		Id:       msg.Id,
		ThreadId: msg.ThreadId,
		Snippet:  msg.Snippet,
		LabelIds: msg.LabelIds,
		Provider: emailv1.Provider_GMAIL,
	}
	if msg.Payload == nil {
		return e
	}
	for _, h := range msg.Payload.Headers {
		switch strings.ToLower(h.Name) {
		case "subject":
			e.Subject = h.Value
		case "from":
			e.From = parseAddress(h.Value)
		case "to":
			e.To = parseAddresses(h.Value)
		case "date":
			if t, err := mail.ParseDate(h.Value); err == nil {
				e.Date = timestamppb.New(t)
			}
		}
	}
	e.Body = extractBody(msg.Payload)
	e.Attachments = extractAttachments(msg.Payload)
	return e
}

func toLabel(l *gmailapi.Label) *emailv1.Label {
	lt := emailv1.LabelType_USER
	if l.Type == "system" {
		lt = emailv1.LabelType_SYSTEM
	}
	return &emailv1.Label{Id: l.Id, Name: l.Name, Type: lt}
}

func parseAddress(raw string) *emailv1.Address {
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return &emailv1.Address{Email: raw}
	}
	return &emailv1.Address{Name: addr.Name, Email: addr.Address}
}

func parseAddresses(raw string) []*emailv1.Address {
	addrs, err := mail.ParseAddressList(raw)
	if err != nil {
		return []*emailv1.Address{{Email: raw}}
	}
	out := make([]*emailv1.Address, len(addrs))
	for i, a := range addrs {
		out[i] = &emailv1.Address{Name: a.Name, Email: a.Address}
	}
	return out
}

// extractBody traverses the MIME tree and returns the first usable text body.
// Preference order: text/plain > text/html > nested multipart.
func extractBody(part *gmailapi.MessagePart) string {
	if part == nil {
		return ""
	}
	// Simple (non-multipart) message
	if len(part.Parts) == 0 && part.Body != nil && part.Body.Data != "" {
		return decodeBase64URL(part.Body.Data)
	}
	for _, p := range part.Parts {
		if p.MimeType == "text/plain" && p.Body != nil && p.Body.Data != "" {
			return decodeBase64URL(p.Body.Data)
		}
	}
	for _, p := range part.Parts {
		if p.MimeType == "text/html" && p.Body != nil && p.Body.Data != "" {
			return decodeBase64URL(p.Body.Data)
		}
	}
	for _, p := range part.Parts {
		if strings.HasPrefix(p.MimeType, "multipart/") {
			if body := extractBody(p); body != "" {
				return body
			}
		}
	}
	return ""
}

func extractAttachments(part *gmailapi.MessagePart) []*emailv1.Attachment {
	if part == nil {
		return nil
	}
	var out []*emailv1.Attachment
	for _, p := range part.Parts {
		if p.Body != nil && p.Body.AttachmentId != "" {
			out = append(out, &emailv1.Attachment{
				Id:       p.Body.AttachmentId,
				Filename: p.Filename,
				MimeType: p.MimeType,
				Size:     p.Body.Size,
			})
		}
		out = append(out, extractAttachments(p)...)
	}
	return out
}

// decodeBase64URL decodes Gmail's base64url-encoded data (no padding).
func decodeBase64URL(s string) string {
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(data)
}
