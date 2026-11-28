package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// sendSMTP delivers one message. TLS is required (STARTTLS or from the first byte) unless the server is on
// this host; a login is offered only over TLS or to a local server (net/smtp refuses otherwise).
func sendSMTP(ctx context.Context, c Connection, to string, msg []byte) error {
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	if c.TLS == "tls" {
		conn = tls.Client(conn, tlsCfg)
	}
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return err
	}
	defer cl.Close()
	if c.TLS == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return &permanentError{"the server does not offer STARTTLS, and the connection is set to require it"}
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return errors.New("the login at the mail server failed: " + firstLine(err.Error()))
		}
	}
	if err := cl.Mail(addrOnly(c.From)); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

// addrOnly returns the address of "Name <a@b>" or "a@b".
func addrOnly(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.Index(from[i:], ">"); j > 0 {
			return from[i+1 : i+j]
		}
	}
	return strings.TrimSpace(from)
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
