package auth

import (
	"bufio"
	"mime"
	"net"
	"net/mail"
	"strings"
	"testing"

	"github.com/rapibase/rapibase/internal/config"
)

// fakeSMTP accepts one message on 127.0.0.1 (net/smtp allows PLAIN auth
// without TLS only on localhost) and returns what it received.
func fakeSMTP(t *testing.T) (port string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got = make(chan string, 1)
	go func() {
		defer ln.Close()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
		w("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					w("250 queued")
					got <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				w("250-fake")
				w("250 AUTH PLAIN")
			case strings.HasPrefix(cmd, "AUTH"):
				w("235 ok")
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				w("354 go")
			case strings.HasPrefix(cmd, "QUIT"):
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, got
}

func TestSMTPDeliverHeaders(t *testing.T) {
	port, got := fakeSMTP(t)
	s := NewSMTPClient(&config.Config{
		SMTPHost: "127.0.0.1", SMTPPort: port, SMTPUser: "u", SMTPPass: "p",
		SMTPFrom: "no-reply@acme.com", SMTPFromName: "Acmé Pagos", AppURL: "https://x",
	})
	if err := s.SendTestEmail("ana@example.com"); err != nil {
		t.Fatal(err)
	}
	raw := <-got
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("unparseable message: %v\n%s", err, raw)
	}
	if subj, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); subj != "Rapibase SMTP test" {
		t.Errorf("Subject: %q", subj)
	}
	from, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil || from.Name != "Acmé Pagos" || from.Address != "no-reply@acme.com" {
		t.Errorf("From: %q (%v)", msg.Header.Get("From"), err)
	}
	for _, h := range []string{"Date", "Message-ID", "Subject"} {
		if msg.Header.Get(h) == "" {
			t.Errorf("missing %s header", h)
		}
	}
	if !strings.HasSuffix(msg.Header.Get("Message-ID"), "@acme.com>") {
		t.Errorf("Message-ID: %q", msg.Header.Get("Message-ID"))
	}
}

func TestSMTPRejectsHeaderInjection(t *testing.T) {
	s := NewSMTPClient(&config.Config{SMTPHost: "127.0.0.1", SMTPPort: "1", SMTPUser: "u", SMTPPass: "p"})
	if err := s.deliver("ana@example.com\r\nBcc: x@evil.com", "s", "b"); err == nil || !strings.Contains(err.Error(), "invalid recipient") {
		t.Errorf("want invalid recipient, got %v", err)
	}
	m := s.buildMessage("a@b.com", "c@d.com", "Hola\r\nBcc: x@evil.com — verificá", "<p>x</p>")
	if strings.Contains(m, "\r\nBcc:") {
		t.Errorf("subject injected a header:\n%s", m)
	}
}

func TestSMTPNotConfigured(t *testing.T) {
	s := NewSMTPClient(&config.Config{})
	if err := s.SendTestEmail("a@b.com"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("got %v", err)
	}
}
