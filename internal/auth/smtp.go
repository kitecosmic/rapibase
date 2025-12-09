package auth

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"

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

	resetURL := fmt.Sprintf("%s/reset-password?token=%s", s.config.AppURL, token)

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

	fromEmail := s.config.SMTPFrom
	if fromEmail == "" {
		fromEmail = s.config.SMTPUser
	}

	// Format From header with name: "Name <email@example.com>"
	fromHeader := fromEmail
	if s.config.SMTPFromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", s.config.SMTPFromName, fromEmail)
	}

	// Build email headers
	headers := make(map[string]string)
	headers["From"] = fromHeader
	headers["To"] = to
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"

	var message strings.Builder
	for k, v := range headers {
		message.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	message.WriteString("\r\n")
	message.WriteString(htmlBody)

	// SMTP authentication
	auth := smtp.PlainAuth("", s.config.SMTPUser, s.config.SMTPPass, s.config.SMTPHost)

	addr := fmt.Sprintf("%s:%s", s.config.SMTPHost, s.config.SMTPPort)
	log.Printf("[SMTP] Sending email to %s via %s", to, addr)

	err := smtp.SendMail(addr, auth, fromEmail, []string{to}, []byte(message.String()))
	if err != nil {
		log.Printf("[SMTP] Error sending email: %v", err)
		return fmt.Errorf("failed to send email: %w", err)
	}

	log.Printf("[SMTP] Email sent successfully to %s", to)
	return nil
}
