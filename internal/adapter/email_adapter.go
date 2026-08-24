package adapter

import (
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type EmailAdapter struct {
	host      string
	port      int
	user      string
	password  string
	fromEmail string
	fromName  string
}

func NewEmailAdapter(cfg *config.AppConfig) *EmailAdapter {
	return &EmailAdapter{
		host:      cfg.SMTPHost,
		port:      cfg.SMTPPort,
		user:      cfg.SMTPUser,
		password:  cfg.SMTPPassword,
		fromEmail: cfg.SMTPFromEmail,
		fromName:  cfg.SMTPFromName,
	}
}

func (e *EmailAdapter) Send(to []string, subject string, body string) error {
	msg := []byte(fmt.Sprintf("From: %s <%s>\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=\"UTF-8\"\r\n"+
		"\r\n"+
		"%s\r\n", e.fromName, e.fromEmail, strings.Join(to, ","), subject, body))

	operation := func() (struct{}, bool, error) {
		timeout := 10 * time.Second
		err := e.sendWithTimeout(to, msg, timeout)

		if err != nil {
			return struct{}{}, true, err
		}

		return struct{}{}, false, nil
	}

	_, err := helper.RetryWithBackoff(operation, 3, 1*time.Second)
	return err
}

func (e *EmailAdapter) sendWithTimeout(to []string, msg []byte, timeout time.Duration) error {
	addr := net.JoinHostPort(e.host, strconv.Itoa(e.port))

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return err
	}

	client, err := smtp.NewClient(conn, e.host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = client.Close() }()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: e.host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}

	if e.user != "" || e.password != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp server does not support AUTH")
		}

		auth := smtp.PlainAuth("", e.user, e.password, e.host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}

	if err := client.Mail(e.fromEmail); err != nil {
		return err
	}

	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}

	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}

	if err := writer.Close(); err != nil {
		return err
	}

	return client.Quit()
}
