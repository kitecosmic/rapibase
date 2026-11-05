package auth

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"html"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/rapibase/rapibase/internal/config"
)

type SMTPClient struct {
	config *config.Config
}

func NewSMTPClient(cfg *config.Config) *SMTPClient {
	return &SMTPClient{config: cfg}
}

// SendPasswordResetEmail sends a password reset email
func (s *SMTPClient) SendPasswordResetEmail(to, token string) error {
	if !s.config.IsSMTPConfigured() {
		return fmt.Errorf("SMTP not configured")
	}

	// The admin dashboard lives under /_/ — link straight there so the reset
	// flow works even when a public site (PUBLIC_DIR) is mounted at /.
	resetURL := fmt.Sprintf("%s/_/reset-password?token=%s", s.config.AppURL, token)

	subject := "Reset your RapiBase password"
	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .button { display: inline-block; padding: 12px 24px; background: #3b82f6; color: white; text-decoration: none; border-radius: 6px; margin: 20px 0; }
        .footer { margin-top: 30px; font-size: 12px; color: #666; }
    </style>
</head>
<body>
    <div class="container">
        <h2>Password Reset Request</h2>
        <p>You requested to reset your password. Click the button below to set a new password:</p>
        <a href="%s" class="button">Reset Password</a>
        <p>Or copy and paste this link into your browser:</p>
        <p style="word-break: break-all; color: #3b82f6;">%s</p>
        <p>This link will expire in 1 hour.</p>
        <p>If you didn't request this, you can safely ignore this email.</p>
        <div class="footer">
            <p>— RapiBase</p>
        </div>
    </div>
</body>
</html>
`, resetURL, resetURL)

	return s.sendEmail(to, subject, body)
}

// SendWelcomeEmail sends a welcome email to new users
func (s *SMTPClient) SendWelcomeEmail(to string) error {
	if !s.config.IsSMTPConfigured() {
		return nil // Silently skip if not configured
	}

	subject := "Welcome to RapiBase"
	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .button { display: inline-block; padding: 12px 24px; background: #3b82f6; color: white; text-decoration: none; border-radius: 6px; margin: 20px 0; }
    </style>
</head>
<body>
    <div class="container">
        <h2>Welcome to RapiBase! 🚀</h2>
        <p>Your account has been created successfully.</p>
        <a href="%s" class="button">Go to Dashboard</a>
        <p>Happy building!</p>
        <p>— RapiBase Team</p>
    </div>
</body>
</html>
`, s.config.AppURL)

	return s.sendEmail(to, subject, body)
}

// SendEmail sends a generic email with subject and plain text body
func (s *SMTPClient) SendEmail(to, subject, body string) error {
	if !s.config.IsSMTPConfigured() {
		return fmt.Errorf("SMTP not configured")
	}

	htmlBody := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .button { display: inline-block; padding: 12px 24px; background: #3b82f6; color: white; text-decoration: none; border-radius: 6px; margin: 20px 0; }
        a { color: #3b82f6; }
    </style>
</head>
<body>
    <div class="container">
        <p>%s</p>
    </div>
</body>
</html>
`, strings.ReplaceAll(body, "\n", "<br>"))

	return s.sendEmail(to, subject, htmlBody)
}

func (s *SMTPClient) sendEmail(to, subject, htmlBody string) error {
	// Check if SMTP is configured
	if s.config.SMTPHost == "" || s.config.SMTPUser == "" || s.config.SMTPPass == "" {
		log.Printf("[SMTP] Not configured - skipping email to %s", to)
		return nil // Don't fail if SMTP not configured
	}
	log.Printf("[SMTP] Sending email to %s via %s:%s", to, s.config.SMTPHost, s.config.SMTPPort)
	if err := s.deliver(to, subject, htmlBody); err != nil {
		log.Printf("[SMTP] Error sending email: %v", err)
		return fmt.Errorf("failed to send email: %w", err)
	}
	log.Printf("[SMTP] Email sent successfully to %s", to)
	return nil
}

// SendTestEmail sends a short message and returns the server's exact error,
// so whoever configures SMTP sees why it fails (auth, port, TLS, sender).
func (s *SMTPClient) SendTestEmail(to string) error {
	if !s.config.IsSMTPConfigured() {
		return fmt.Errorf("SMTP not configured: SMTP_HOST, SMTP_USER and SMTP_PASS are required")
	}
	body := `<!DOCTYPE html><html><body style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;color:#333">
<p>This is a test email from your Rapibase instance.</p>
<p>If you are reading it, SMTP works: verification, magic link and password reset emails will be delivered.</p>
<p style="color:#888;font-size:12px">` + html.EscapeString(s.config.AppURL) + `</p></body></html>`
	return s.deliver(to, "Rapibase SMTP test", body)
}

// buildMessage renders headers + body. Subject and sender name are RFC 2047
// encoded (accents survive), CR/LF can't inject headers, and Date and
// Message-ID are set (their absence raises spam scores).
func (s *SMTPClient) buildMessage(fromEmail, to, subject, htmlBody string) string {
	from := (&mail.Address{Name: s.config.SMTPFromName, Address: fromEmail}).String()
	domain := "rapibase.local"
	if i := strings.LastIndex(fromEmail, "@"); i >= 0 {
		domain = fromEmail[i+1:]
	}
	idBytes := make([]byte, 16)
	_, _ = rand.Read(idBytes)
	oneLine := strings.NewReplacer("\r", " ", "\n", " ")

	var msg strings.Builder
	for _, h := range [][2]string{
		{"From", from},
		{"To", oneLine.Replace(to)},
		{"Subject", mime.QEncoding.Encode("utf-8", oneLine.Replace(subject))},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", fmt.Sprintf("<%s@%s>", hex.EncodeToString(idBytes), domain)},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/html; charset=UTF-8"},
		{"Content-Transfer-Encoding", "8bit"},
	} {
		msg.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	msg.WriteString("\r\n")
	msg.WriteString(strings.ReplaceAll(strings.ReplaceAll(htmlBody, "\r\n", "\n"), "\n", "\r\n"))
	return msg.String()
}

// deliver speaks SMTP with timeouts: implicit TLS on port 465, STARTTLS on
// any other port when the server offers it (net/smtp refuses to send AUTH
// in clear text to a remote host).
func (s *SMTPClient) deliver(to, subject, htmlBody string) error {
	if strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("invalid recipient")
	}
	host := s.config.SMTPHost
	port := s.config.SMTPPort
	if port == "" {
		port = "587"
	}
	fromEmail := s.config.SMTPFrom
	if fromEmail == "" {
		fromEmail = s.config.SMTPUser
	}
	if strings.ContainsAny(fromEmail, "\r\n") {
		return fmt.Errorf("invalid SMTP_FROM")
	}
	msg := s.buildMessage(fromEmail, to, subject, htmlBody)

	addr := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if port == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP greeting from %s: %w", addr, err)
	}
	defer c.Close()
	if port != "465" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		}
	}
	if ok, _ := c.Extension("AUTH"); ok {
		if err := c.Auth(smtp.PlainAuth("", s.config.SMTPUser, s.config.SMTPPass, host)); err != nil {
			return fmt.Errorf("authentication: %w", err)
		}
	}
	if err := c.Mail(fromEmail); err != nil {
		return fmt.Errorf("sender %s rejected: %w", fromEmail, err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("recipient %s rejected: %w", to, err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("message rejected: %w", err)
	}
	return c.Quit()
}
