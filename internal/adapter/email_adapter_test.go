package adapter

import (
	"AtoiTalkAPI/internal/config"
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestEmailAdapterSendsMessage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer func() { _ = listener.Close() }()

	message := make(chan string, 1)
	go serveSMTPOnce(listener, message)

	port := listener.Addr().(*net.TCPAddr).Port
	adapter := NewEmailAdapter(&config.AppConfig{
		SMTPHost:      "127.0.0.1",
		SMTPPort:      port,
		SMTPFromEmail: "from@example.com",
		SMTPFromName:  "Test Sender",
	})
	if err := adapter.Send([]string{"to@example.com"}, "Subject", "<p>Hello</p>"); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	select {
	case got := <-message:
		for _, expected := range []string{
			"From: Test Sender <from@example.com>",
			"To: to@example.com",
			"Subject: Subject",
			"<p>Hello</p>",
		} {
			if !strings.Contains(got, expected) {
				t.Fatalf("message missing %q: %s", expected, got)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP message")
	}
}

func TestEmailAdapterRetriesUnavailableServer(t *testing.T) {
	adapter := NewEmailAdapter(&config.AppConfig{
		SMTPHost:      "127.0.0.1",
		SMTPPort:      1,
		SMTPFromEmail: "from@example.com",
	})

	err := adapter.Send([]string{"to@example.com"}, "Subject", "body")
	if err == nil {
		t.Fatal("expected SMTP connection error")
	}
}

func TestEmailAdapterRejectsAuthWhenServerDoesNotAdvertiseIt(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer func() { _ = listener.Close() }()

	done := make(chan struct{})
	go serveSMTPWithoutAuth(listener, done)

	port := listener.Addr().(*net.TCPAddr).Port
	adapter := NewEmailAdapter(&config.AppConfig{
		SMTPHost:      "127.0.0.1",
		SMTPPort:      port,
		SMTPUser:      "user",
		SMTPPassword:  "password",
		SMTPFromEmail: "from@example.com",
	})
	err = adapter.sendWithTimeout([]string{"to@example.com"}, []byte("body"), time.Second)
	if err == nil || !strings.Contains(err.Error(), "does not support AUTH") {
		t.Fatalf("expected AUTH capability error, got %v", err)
	}
	<-done
}

func serveSMTPOnce(listener net.Listener, message chan<- string) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	writeSMTP := func(format string, args ...interface{}) {
		_, _ = fmt.Fprintf(conn, format+"\r\n", args...)
	}
	writeSMTP("220 localhost ESMTP")

	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			writeSMTP("250-localhost")
			writeSMTP("250 OK")
		case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			writeSMTP("250 OK")
		case command == "DATA":
			writeSMTP("354 End data with <CR><LF>.<CR><LF>")
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				if line == "." {
					break
				}
				data.WriteString(line)
				data.WriteByte('\n')
			}
			writeSMTP("250 OK")
		case command == "QUIT":
			writeSMTP("221 Bye")
			message <- data.String()
			return
		default:
			writeSMTP("250 OK")
		}
	}
}

func TestEmailAdapterAuthenticatesAndSendsMessage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer func() { _ = listener.Close() }()

	message := make(chan string, 1)
	go serveSMTPWithAuth(listener, message)

	port := listener.Addr().(*net.TCPAddr).Port
	adapter := NewEmailAdapter(&config.AppConfig{
		SMTPHost:      "127.0.0.1",
		SMTPPort:      port,
		SMTPUser:      "user",
		SMTPPassword:  "password",
		SMTPFromEmail: "from@example.com",
		SMTPFromName:  "Auth Sender",
	})
	if err := adapter.Send([]string{"to@example.com"}, "Auth Subject", "<p>Authenticated</p>"); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	select {
	case got := <-message:
		if !strings.Contains(got, "<p>Authenticated</p>") {
			t.Fatalf("message missing body: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for authenticated SMTP message")
	}
}

func serveSMTPWithAuth(listener net.Listener, message chan<- string) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	writeSMTP := func(format string, args ...interface{}) {
		_, _ = fmt.Fprintf(conn, format+"\r\n", args...)
	}
	writeSMTP("220 localhost ESMTP")

	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			writeSMTP("250-localhost")
			writeSMTP("250-AUTH PLAIN")
			writeSMTP("250 OK")
		case strings.HasPrefix(command, "AUTH PLAIN"):
			writeSMTP("235 2.7.0 Authentication successful")
		case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			writeSMTP("250 OK")
		case command == "DATA":
			writeSMTP("354 End data with <CR><LF>.<CR><LF>")
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				if line == "." {
					break
				}
				data.WriteString(line)
				data.WriteByte('\n')
			}
			writeSMTP("250 OK")
		case command == "QUIT":
			writeSMTP("221 Bye")
			message <- data.String()
			return
		default:
			writeSMTP("250 OK")
		}
	}
}

func serveSMTPWithoutAuth(listener net.Listener, done chan<- struct{}) {
	defer close(done)
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	writeSMTP := func(format string, args ...interface{}) {
		_, _ = fmt.Fprintf(conn, format+"\r\n", args...)
	}
	writeSMTP("220 localhost ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch strings.TrimRight(line, "\r\n") {
		case "EHLO localhost", "HELO localhost":
			writeSMTP("250-localhost")
			writeSMTP("250 OK")
		default:
			writeSMTP("250 OK")
		}
	}
}
