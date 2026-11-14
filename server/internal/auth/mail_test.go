package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dikym/terka-api/server/internal/auth"
)

const link = "https://terka.my.id/masuk#email=ani%40contoh.id&code=042917"

func TestCodeMailContainsCodeAndLink(t *testing.T) {
	m, err := auth.CodeMail("https://terka.my.id", "ani@contoh.id", "042917")
	if err != nil {
		t.Fatal(err)
	}
	if m.To != "ani@contoh.id" || m.Subject != "Kode masuk Terka" {
		t.Fatalf("to %q, subject %q", m.To, m.Subject)
	}
	for name, body := range map[string]string{"text": m.Text, "html": m.HTML} {
		if !strings.Contains(body, "042917") {
			t.Errorf("%s has no code:\n%s", name, body)
		}
		if !strings.Contains(body, "hanya bisa dipakai di browser tempat Anda memintanya") {
			t.Errorf("%s does not say the code is bound to the browser:\n%s", name, body)
		}
	}
	if !strings.Contains(m.Text, link) {
		t.Errorf("text has no link %s:\n%s", link, m.Text)
	}
	if !strings.Contains(m.HTML, `href="`+strings.ReplaceAll(link, "&", "&amp;")+`"`) {
		t.Errorf("html has no link %s:\n%s", link, m.HTML)
	}
}

func TestCodeMailEscapesTheAddressInTheLink(t *testing.T) {
	m, err := auth.CodeMail("https://terka.my.id", `a+b&code=1"<x>@contoh.id`, "042917")
	if err != nil {
		t.Fatal(err)
	}
	want := "#email=a%2Bb%26code%3D1%22%3Cx%3E%40contoh.id&code=042917"
	if !strings.Contains(m.Text, want) {
		t.Errorf("text link not escaped, want %s:\n%s", want, m.Text)
	}
	if strings.Contains(m.HTML, "<x>") || strings.Contains(m.HTML, `1"`) {
		t.Errorf("html carries the raw address:\n%s", m.HTML)
	}
}

type mailpitMessage struct {
	ID      string
	Subject string
	To      []struct{ Address string }
	Text    string
	HTML    string
}

// mailpitFind waits for the one message sent to address. The inbox is shared, so nothing is deleted.
func mailpitFind(t *testing.T, address string) mailpitMessage {
	t.Helper()
	q := url.QueryEscape(`to:"` + address + `"`)
	for range 50 {
		var found struct{ Messages []mailpitMessage }
		getJSON(t, "http://127.0.0.1:8025/api/v1/search?query="+q, &found)
		if len(found.Messages) == 1 {
			var full mailpitMessage
			getJSON(t, "http://127.0.0.1:8025/api/v1/message/"+found.Messages[0].ID, &full)
			return full
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mailpit: no message to %s", address)
	return mailpitMessage{}
}

func getJSON(t *testing.T, u string, v any) {
	t.Helper()
	res, err := http.Get(u)
	if err != nil {
		t.Fatalf("mailpit is not reachable (start it with make db): %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatalf("mailpit %s: %v", u, err)
	}
}

func TestSMTPMailerDeliversToMailpit(t *testing.T) {
	mailer, err := auth.NewSMTPMailer("smtp://127.0.0.1:1025", "Terka <no-reply@localhost>")
	if err != nil {
		t.Fatal(err)
	}
	to := fmt.Sprintf("mailtest-%d@contoh.id", time.Now().UnixNano())
	m, err := auth.CodeMail("http://localhost:5173", to, "042917")
	if err != nil {
		t.Fatal(err)
	}
	if err := mailer.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	got := mailpitFind(t, to)
	if got.Subject != "Kode masuk Terka" || len(got.To) != 1 || got.To[0].Address != to {
		t.Fatalf("subject %q, to %v", got.Subject, got.To)
	}
	if !strings.Contains(got.Text, "042917") || !strings.Contains(got.HTML, "042917") {
		t.Fatalf("code missing:\ntext %s\nhtml %s", got.Text, got.HTML)
	}
}

func TestSMTPMailerRefusesPlainSMTPToARemoteHost(t *testing.T) {
	for _, u := range []string{"smtp://smtp.resend.com:587", "smtp://203.0.113.7:25", "ftp://localhost:21", "smtp+starttls://smtp.resend.com"} {
		if _, err := auth.NewSMTPMailer(u, "Terka <no-reply@terka.my.id>"); err == nil {
			t.Errorf("%s: accepted", u)
		}
	}
	if _, err := auth.NewSMTPMailer("smtp+starttls://u:p@smtp.resend.com:587", "not an address"); err == nil {
		t.Error("bad from: accepted")
	}
}

func TestMailHeadersCannotBeInjected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dialed atomic.Bool
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			dialed.Store(true)
			_ = conn.Close()
		}
	}()
	mailer, err := auth.NewSMTPMailer("smtp://"+ln.Addr().String(), "Terka <no-reply@localhost>")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []auth.Mail{
		{To: "a@contoh.id\r\nBcc: korban@contoh.id", Subject: "x", Text: "x", HTML: "x"},
		{To: "a@contoh.id", Subject: "x\nBcc: korban@contoh.id", Text: "x", HTML: "x"},
		{To: "Ani <a@contoh.id>, b@contoh.id", Subject: "x", Text: "x", HTML: "x"},
	} {
		if err := mailer.Send(context.Background(), m); err == nil {
			t.Errorf("%q / %q: sent", m.To, m.Subject)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if dialed.Load() {
		t.Fatal("the mailer connected before refusing the headers")
	}
}
