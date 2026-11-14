package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"
)

const (
	codeSubject = "Kode masuk Terka"
	sendTimeout = 30 * time.Second
)

//go:embed templates
var templates embed.FS

var (
	codeText = texttemplate.Must(texttemplate.ParseFS(templates, "templates/code.txt"))
	codeHTML = htmltemplate.Must(htmltemplate.ParseFS(templates, "templates/code.html"))
)

type Mail struct{ To, Subject, Text, HTML string }

type Mailer interface {
	Send(ctx context.Context, m Mail) error
}

// CodeMail is the sign-in email. The link carries the code in the fragment, which browsers never
// send to a server, so it stays out of access logs.
func CodeMail(publicURL, email, code string) (Mail, error) {
	if !sixDigits(code) {
		return Mail{}, errors.New("mail: code must be six digits")
	}
	data := struct{ Code, Link string }{
		Code: code,
		Link: publicURL + "/masuk#email=" + url.QueryEscape(email) + "&code=" + code,
	}
	var text, html bytes.Buffer
	if err := codeText.Execute(&text, data); err != nil {
		return Mail{}, err
	}
	if err := codeHTML.Execute(&html, data); err != nil {
		return Mail{}, err
	}
	return Mail{To: email, Subject: codeSubject, Text: text.String(), HTML: html.String()}, nil
}

type smtpMailer struct {
	addr     string
	host     string
	starttls bool
	auth     smtp.Auth
	from     *mail.Address
}

// NewSMTPMailer sends through smtp+starttls://user:pass@host:port, which must offer STARTTLS, or
// through plain smtp://host:port to Mailpit: on this machine, or anywhere when local is set, for the
// containerised local stack.
func NewSMTPMailer(smtpURL, from string, local bool) (Mailer, error) {
	// url.Parse errors quote the whole URL, password included, so they are not passed on.
	u, err := url.Parse(smtpURL)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return nil, errors.New("mail: SMTP URL must look like smtp+starttls://user:pass@host:port")
	}
	m := &smtpMailer{addr: u.Host, host: u.Hostname()}
	switch u.Scheme {
	case "smtp+starttls":
		m.starttls = true
	case "smtp":
		if ip := net.ParseIP(m.host); !local && m.host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("mail: plain smtp:// is only allowed to this machine")
		}
	default:
		return nil, errors.New("mail: SMTP URL scheme must be smtp+starttls or smtp")
	}
	if u.User != nil {
		pass, _ := u.User.Password()
		// PlainAuth itself refuses to send the password over a connection that is not TLS.
		m.auth = smtp.PlainAuth("", u.User.Username(), pass, m.host)
	}
	if m.from, err = mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("mail: from address: %w", err)
	}
	return m, nil
}

func (s *smtpMailer) Send(ctx context.Context, m Mail) error {
	// The recipient must be one bare address, so nothing in it can become another header or recipient.
	to, err := mail.ParseAddress(m.To)
	if err != nil || to.Name != "" || to.Address != m.To {
		return errors.New("mail: recipient must be a single plain address")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mail: subject must be one line")
	}
	msg, err := s.message(m)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return err
	}
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: %w", err)
	}
	defer func() { _ = c.Close() }()
	if s.starttls {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("mail: server does not offer STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if s.auth != nil {
		if err := c.Auth(s.auth); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mail: rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	return c.Quit()
}

func (s *smtpMailer) message(m Mail) ([]byte, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range []struct{ kind, content string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.kind + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qw := quotedprintable.NewWriter(pw)
		if _, err := qw.Write([]byte(part.content)); err != nil {
			return nil, err
		}
		if err := qw.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	domain := s.from.Address[strings.LastIndex(s.from.Address, "@")+1:]
	var msg bytes.Buffer
	for _, h := range [][2]string{
		{"From", s.from.String()},
		{"To", m.To},
		{"Subject", mime.QEncoding.Encode("utf-8", m.Subject)},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", "<" + rand.Text() + "@" + domain + ">"},
		{"Auto-Submitted", "auto-generated"},
		{"MIME-Version", "1.0"},
		{"Content-Type", `multipart/alternative; boundary="` + mw.Boundary() + `"`},
	} {
		msg.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	msg.WriteString("\r\n")
	msg.Write(body.Bytes())
	return msg.Bytes(), nil
}
