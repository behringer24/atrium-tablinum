package notify

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

// kindTest is the notice of admin_notifications action=test. It is no event of Kinds.
const kindTest Kind = "test"

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// line is the sentence for one delivery. space is "" when the space is not named. A notice says that
// something waits and never what it is.
func line(d delivery, space string) string {
	in := ""
	if space != "" {
		in = " in the space " + space
	}
	switch d.kind {
	case DraftWaiting:
		return fmt.Sprintf("%d %s for review%s.", d.count, plural(d.count, "draft waits", "drafts wait"), in)
	case DraftApproved:
		return fmt.Sprintf("%d of your %s approved%s.", d.count, plural(d.count, "drafts was", "drafts were"), in)
	case DraftReturned:
		return fmt.Sprintf("%d of your %s returned to you with a note%s.", d.count, plural(d.count, "drafts was", "drafts were"), in)
	case ItemReceived:
		return fmt.Sprintf("%d %s for you to review.", d.count, plural(d.count, "item was sent to your personal space and waits", "items were sent to your personal space and wait"))
	case HealthChanged:
		name, state, _ := strings.Cut(d.detail, " ")
		if state == "ok" {
			return fmt.Sprintf("The health check %s is ok again.", name)
		}
		return fmt.Sprintf("The health check %s is failing. Ask your assistant for the system health.", name)
	case kindTest:
		return "This is a test notice. Nothing is wrong."
	}
	return "Something changed."
}

// spaceName returns the name of the space of a delivery, "" if there is none.
func (s *Service) spaceName(ctx context.Context, id uuid.UUID) string {
	if id == uuid.Nil || s.spaces == nil {
		return ""
	}
	sp, err := s.spaces.GetByID(ctx, id)
	if err != nil || sp.Type == "personal" {
		return ""
	}
	return sp.Name
}

// mailMessage builds one message with a line for each delivery due for the person. The recipient was found
// by the role that lets them see the space, so the space is named.
func (s *Service) mailMessage(ctx context.Context, c Connection, u users.User, ds []delivery) ([]byte, error) {
	var body bytes.Buffer
	for _, d := range ds {
		fmt.Fprintf(&body, "- %s\r\n", line(d, s.spaceName(ctx, d.space)))
	}
	subject := s.opt.Instance + ": " + line(ds[0], "")
	if len(ds) > 1 {
		subject = fmt.Sprintf("%s: %d notices", s.opt.Instance, len(ds))
	}
	return buildMail(s.now(), s.opt.Instance, c.From, u.Email, subject, body.String(), "Atrium"), nil
}

// buildMail is a plain-text message. Header values are cleaned of line breaks, so that nothing a person
// can write ends up as a header of its own.
func buildMail(now time.Time, instance, from, to, subject, body, product string) []byte {
	clean := func(v string) string {
		return strings.Join(strings.FieldsFunc(v, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
	}
	var b bytes.Buffer
	domain := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		domain = strings.Trim(clean(from[i+1:]), "<> ")
	}
	fmt.Fprintf(&b, "From: %s\r\n", clean(from))
	fmt.Fprintf(&b, "To: %s\r\n", clean(to))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", clean(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", uuid.NewString(), domain)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(body + "\r\nThis message names no content. Ask your assistant what waits for you, for example: \"What is new in " + product + "?\"\r\n"))
	_ = qp.Close()
	return b.Bytes()
}
