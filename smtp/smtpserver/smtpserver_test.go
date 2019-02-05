package smtpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
	"time"

	"spilled.ink/tlstest"
)

func listen(t *testing.T) net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if ln, err = net.Listen("tcp6", "[::1]:0"); err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
	}
	return ln
}

func TestNoTLS(t *testing.T) {
	ln := listen(t)
	errCh := make(chan error)
	server := &Server{
		Hostname:  "testing",
		Logf:      t.Logf,
		TLSConfig: tlstest.ServerConfig,
	}
	go func() {
		errCh <- server.ServeSTARTTLS(ln)
	}()

	time.Sleep(5 * time.Millisecond)
	c, err := smtp.Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Noop(); err != nil {
		t.Error(err)
	}
	if err := c.Noop(); err != nil {
		t.Error(err)
	}
	if err := c.Mail("foo@example.com"); err == nil {
		t.Error("want STARTTLS error, got nothing")
	} else {
		te, ok := err.(*textproto.Error)
		if !ok || te.Code != 530 || !strings.Contains(te.Msg, "STARTTLS") {
			t.Errorf("want STARTTLS error, got: %v", err)
		}
	}
	if err := c.Quit(); err != nil {
		t.Error(err)
	}
	server.Shutdown(context.Background())
	if err := <-errCh; err != ErrServerClosed {
		t.Errorf("ServeSTARTTLS: %v, want ErrServerClosed", err)
	}
}

type memMsg struct {
	from       string
	recipients []string
	body       bytes.Buffer
	cancelled  bool
	closed     bool
}

func (m *memMsg) AddRecipient(addr []byte) (bool, error) {
	m.recipients = append(m.recipients, string(addr))
	return true, nil
}

func (m *memMsg) Write(line []byte) error {
	if m.closed {
		return errors.New("memMsg closed")
	}
	m.body.Write(line)
	return nil
}

func (m *memMsg) Cancel() {
	m.cancelled = true
}

func (m *memMsg) Close() error {
	m.closed = true
	return nil
}

func TestSend(t *testing.T) {
	msg := new(memMsg)
	ln := listen(t)
	errCh := make(chan error)
	server := &Server{
		Hostname: "testing",
		NewMessage: func(_ net.Addr, addr []byte, authToken uint64) (Msg, error) {
			msg.from = string(addr)
			return msg, nil
		},
		Logf:      t.Logf,
		TLSConfig: tlstest.ServerConfig,
	}
	go func() {
		errCh <- server.ServeSTARTTLS(ln)
	}()

	time.Sleep(5 * time.Millisecond)
	c, err := smtp.Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Noop(); err != nil {
		t.Error(err)
	}
	config := &tls.Config{InsecureSkipVerify: true}
	if err := c.StartTLS(config); err != nil {
		t.Error(err)
	}
	if err := c.Noop(); err != nil {
		t.Error(err)
	}
	const from = "from@example.com"
	if err := c.Mail(from); err != nil {
		t.Error(err)
	}
	const to1 = "to1@example.from"
	if err := c.Rcpt(to1); err != nil {
		t.Error(err)
	}
	const to2 = "to2@example.from"
	if err := c.Rcpt(to2); err != nil {
		t.Error(err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	const data = "hello\n.world!"
	if _, err := w.Write([]byte(data)); err != nil {
		t.Error(err)
	}
	if err := w.Close(); err != nil {
		t.Error(err)
	}
	if err := c.Quit(); err != nil {
		t.Error(err)
	}
	server.Shutdown(context.Background())
	if err := <-errCh; err != ErrServerClosed {
		t.Errorf("ServeSTARTTLS: %v, want ErrServerClosed", err)
	}

	if msg.from != from {
		t.Errorf("from=%q, want %q", msg.from, from)
	}
	if want := []string{to1, to2}; !reflect.DeepEqual(msg.recipients, want) {
		t.Errorf("recipients: %v, want %v", msg.recipients, want)
	}
}

func TestMaxSize(t *testing.T) {
	msg := new(memMsg)
	ln := listen(t)
	errCh := make(chan error)
	server := &Server{
		Hostname: "testing",
		MaxSize:  20,
		NewMessage: func(_ net.Addr, addr []byte, authToken uint64) (Msg, error) {
			msg.from = string(addr)
			return msg, nil
		},
		Logf:      t.Logf,
		TLSConfig: tlstest.ServerConfig,
	}
	go func() {
		errCh <- server.ServeSTARTTLS(ln)
	}()

	time.Sleep(5 * time.Millisecond)
	c, err := smtp.Dial(ln.Addr().String())
	c.StartTLS(&tls.Config{InsecureSkipVerify: true})
	c.Mail("from@example.com")
	c.Rcpt("to@example.from")
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	w.Write(make([]byte, 100))
	if err := w.Close(); err == nil {
		t.Error("write succeeded, expected failure")
	} else if !strings.Contains(err.Error(), "Too much") {
		t.Errorf("failure does not mention 'Too much': %v", err)
	}
	server.Shutdown(context.Background())
}

func TestMaxRecipients(t *testing.T) {
	msg := new(memMsg)
	ln := listen(t)
	server := &Server{
		Hostname:      "testing",
		MaxRecipients: 3,
		NewMessage: func(_ net.Addr, addr []byte, authToken uint64) (Msg, error) {
			msg.from = string(addr)
			return msg, nil
		},
		Logf:      t.Logf,
		TLSConfig: tlstest.ServerConfig,
	}
	go server.ServeSTARTTLS(ln)

	time.Sleep(5 * time.Millisecond)
	c, err := smtp.Dial(ln.Addr().String())
	c.StartTLS(&tls.Config{InsecureSkipVerify: true})
	c.Mail("from@example.com")
	for i := 0; i < 5; i++ {
		e := c.Rcpt("to@example.from")
		if err == nil {
			err = e
		}
	}
	if err == nil {
		t.Error("RCPT succeeded, expected failure")
	} else if !strings.Contains(err.Error(), "Too many recipients") {
		t.Errorf("RCPT failure does not mention 'recipients': %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server.Shutdown(ctx)
}

func TestTLS(t *testing.T) {
	msg := new(memMsg)
	ln := listen(t)
	errCh := make(chan error)
	server := &Server{
		Hostname: "localhost",
		MaxSize:  20,
		NewMessage: func(_ net.Addr, addr []byte, authToken uint64) (Msg, error) {
			msg.from = string(addr)
			return msg, nil
		},
		Logf:      t.Logf,
		TLSConfig: tlstest.ServerConfig,
	}
	go func() {
		errCh <- server.ServeTLS(ln)
	}()

	time.Sleep(5 * time.Millisecond)

	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if err := c.Mail("from@example.com"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	server.Shutdown(context.Background())

	if err := <-errCh; err != ErrServerClosed {
		t.Fatal(err)
	}
}
func TestAuth(t *testing.T) {
	var msgAuthToken uint64
	msg := new(memMsg)

	ln := listen(t)
	server := &Server{
		Hostname: "localhost",
		MaxSize:  20,
		NewMessage: func(_ net.Addr, addr []byte, authToken uint64) (Msg, error) {
			msgAuthToken = authToken
			msg.from = string(addr)
			return msg, nil
		},
		Auth: func(identity, user, pass []byte, remoteAddr string) uint64 {
			if string(user) == "bob" && string(pass) == "secret" {
				return 7
			}
			return 0
		},
		MustAuth:  true,
		Logf:      t.Logf,
		TLSConfig: tlstest.ServerConfig,
	}
	defer server.Shutdown(context.Background())
	go server.ServeSTARTTLS(ln)

	time.Sleep(5 * time.Millisecond)

	newClient := func(t *testing.T) *smtp.Client {
		c, err := smtp.Dial(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.StartTLS(&tls.Config{InsecureSkipVerify: true}); err != nil {
			c.Close()
			t.Fatal(err)
		}
		return c
	}

	t.Run("noauth", func(t *testing.T) {
		c := newClient(t)
		defer c.Close()

		// An auth function means we must authenticate.
		if err := c.Mail("from@example.com"); err != nil {
			tpErr, _ := err.(*textproto.Error)
			if tpErr == nil {
				t.Errorf("error has no SMTP code: %v", err)
			} else if tpErr.Code != 530 {
				t.Errorf("want err 530, got: %v", err)
			}
		} else {
			t.Error("expected error on unauthenticated mail")
		}
	})

	t.Run("mismatched identity and username", func(t *testing.T) {
		c := newClient(t)
		defer c.Close()
		if err := c.Auth(smtp.PlainAuth("jane", "bob", "secret", "localhost")); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("bad username", func(t *testing.T) {
		c := newClient(t)
		defer c.Close()
		if err := c.Auth(smtp.PlainAuth("jane", "jane", "secret", "localhost")); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("bad password", func(t *testing.T) {
		c := newClient(t)
		defer c.Close()
		if err := c.Auth(smtp.PlainAuth("bob", "bob", "notsecret", "localhost")); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("bad host", func(t *testing.T) {
		c := newClient(t)
		defer c.Close()
		if err := c.Auth(smtp.PlainAuth("bob", "bob", "secret", "smtp.google.com")); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("send mail", func(t *testing.T) {
		c := newClient(t)
		defer c.Close()
		if err := c.Auth(smtp.PlainAuth("bob", "bob", "secret", "127.0.0.1")); err != nil {
			t.Errorf("auth error: %v", err)
		}
		if err := c.Mail("from@example.com"); err != nil {
			t.Errorf("sending mail failed after auth: %v", err)
		}

		if msgAuthToken != 7 {
			t.Errorf("msgAuthToken=%v, want 7", msgAuthToken)
		}
	})
	t.Run("LOGIN", func(t *testing.T) {
		c := newClient(t)
		defer c.Close()

		c.Text.Writer.W.WriteString("AUTH LOGIN\r\n")
		c.Text.Writer.W.Flush()
		_, msg, err := c.Text.ReadCodeLine(334)
		if err != nil {
			t.Fatal(err)
		}
		if msg != "VXNlcm5hbWU6AA==" {
			t.Errorf("unexpected message, want base64 for 'Username' got %q", msg)
		}

		fmt.Fprintf(c.Text.Writer.W, "%s\r\n", base64.StdEncoding.EncodeToString([]byte("bob")))
		c.Text.Writer.W.Flush()
		if _, msg, err := c.Text.ReadCodeLine(334); err != nil {
			t.Fatal(err)
		} else {
			passMsg, err := base64.StdEncoding.DecodeString(msg)
			if err != nil {
				t.Fatal(err)
			}
			if len(passMsg) == 0 || passMsg[len(passMsg)-1] != '\x00' {
				t.Error("macOS Mail requires encoded 'Password:' message be NUL terminated")
			}
		}

		fmt.Fprintf(c.Text.Writer.W, "%s\r\n", base64.StdEncoding.EncodeToString([]byte("secret")))
		c.Text.Writer.W.Flush()
		if _, _, err := c.Text.ReadCodeLine(235); err != nil {
			t.Fatal(err)
		}
	})
}
