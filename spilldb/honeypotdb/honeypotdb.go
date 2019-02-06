// Package honeypotdb wraps an smtpserver NewMessage and
// collects any attempts to authenticate as spam messages.
//
// An SMTP server on port 25 should not accept mail submission
// any more, but lots of AUTH requests still come in.
// This package collects those requests for future study.
package honeypotdb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/smtp/smtpserver"
)

type Honeypot struct {
	ctx             context.Context
	dbpool          *sqlitex.Pool
	filer           *iox.Filer
	wrappedNewMsgFn smtpserver.NewMessageFunc

	mu   sync.Mutex
	auth map[uint64]auth
}

func (h *Honeypot) cleanup() {
	t := time.NewTicker(125 * time.Second)
	for {
		select {
		case <-h.ctx.Done():
			t.Stop()
			break
		case <-t.C:
			h.mu.Lock()
			for token, a := range h.auth {
				if time.Since(a.t) > 120*time.Second {
					delete(h.auth, token)
				}
			}
			h.mu.Unlock()
		}
	}
}

type auth struct {
	t          time.Time
	identity   string
	user       string
	pass       string
	remoteAddr string
}

func New(ctx context.Context, dbpool *sqlitex.Pool, filer *iox.Filer, newMsgFn smtpserver.NewMessageFunc) (*Honeypot, error) {
	conn := dbpool.Get(ctx)
	if conn == nil {
		return nil, context.Canceled
	}
	defer dbpool.Put(conn)

	err := sqlitex.ExecTransient(conn, `CREATE TABLE IF NOT EXISTS Honeypot (
		HoneypotID INTEGER PRIMARY KEY,
		Date       INTEGER NOT NULL,
		RemoteAddr TEXT NOT NULL,
		FromAddr   TEXT NOT NULL,
		Recipients TEXT NOT NULL, -- JSON array of strings
		Identity   TEXT NOT NULL,
		User       TEXT NOT NULL,
		Password   TEXT NOT NULL,
		Contents   BLOB NOT NULL
	);`, nil)
	if err != nil {
		return nil, err
	}

	h := &Honeypot{
		ctx:             ctx,
		dbpool:          dbpool,
		filer:           filer,
		wrappedNewMsgFn: newMsgFn,
		auth:            make(map[uint64]auth),
	}
	go h.cleanup()
	return h, nil
}

func (h *Honeypot) Auth(identity, user, pass []byte, remoteAddr string) uint64 {
	h.mu.Lock()
	var token uint64
	for token == 0 || !h.auth[token].t.IsZero() {
		token = rand.Uint64()
	}
	h.auth[token] = auth{
		t:          time.Now(),
		identity:   string(identity),
		user:       string(user),
		pass:       string(pass),
		remoteAddr: remoteAddr,
	}
	h.mu.Unlock()

	// Any auth request succeeds.
	time.Sleep(2 * time.Second) // malicious client delay
	return token
}

func (h *Honeypot) NewMessage(remoteAddr net.Addr, from []byte, token uint64) (smtpserver.Msg, error) {
	if token == 0 {
		// This is a real message.
		return h.wrappedNewMsgFn(remoteAddr, from, 0)
	}

	h.mu.Lock()
	a := h.auth[token]
	delete(h.auth, token)
	h.mu.Unlock()

	return &msg{
		ctx:        h.ctx,
		dbpool:     h.dbpool,
		f:          h.filer.BufferFile(0),
		auth:       a,
		remoteAddr: remoteAddr,
		from:       string(from),
	}, nil
}

type msg struct {
	ctx        context.Context
	dbpool     *sqlitex.Pool
	f          *iox.BufferFile
	rcpts      []string
	remoteAddr net.Addr
	auth       auth
	from       string
}

func (m *msg) AddRecipient(addr []byte) (bool, error) {
	// Pretend to be an open relay.
	m.rcpts = append(m.rcpts, string(addr))
	time.Sleep(time.Second / 2) // malicious client delay
	return true, nil
}

func (m *msg) Write(line []byte) error {
	time.Sleep(50 * time.Millisecond) // malicious client delay
	_, err := m.f.Write(line)
	return err
}

func (m *msg) Cancel() {
	m.f.Close()
	m.rcpts = nil
}

func (m *msg) Close() error {
	defer time.Sleep(2 * time.Second) // malicious client delay
	defer m.f.Close()

	if _, err := m.f.Seek(0, 0); err != nil {
		return err
	}

	rcpts := new(bytes.Buffer)
	rcpts.WriteByte('[')
	for i, rcpt := range m.rcpts {
		if i > 0 {
			rcpts.WriteString(", ")
		}
		fmt.Fprintf(rcpts, "%q", rcpt)
	}
	rcpts.WriteByte(']')

	conn := m.dbpool.Get(m.ctx)
	if conn == nil {
		return context.Canceled
	}
	defer m.dbpool.Put(conn)

	stmt := conn.Prep(`INSERT INTO Honeypot (
			Date, RemoteAddr, FromAddr, Recipients, Identity, User, Password, Contents   
		) VALUES (
			$date, $remoteAddr, $fromAddr, $recipients, $identity, $user, $password, $contents
		);`)
	stmt.SetInt64("$date", m.auth.t.Unix())
	stmt.SetText("$remoteAddr", m.auth.remoteAddr)
	stmt.SetText("$fromAddr", m.from)
	stmt.SetBytes("$recipients", rcpts.Bytes())
	stmt.SetText("$identity", m.auth.identity)
	stmt.SetText("$user", m.auth.user)
	stmt.SetText("$password", m.auth.pass)
	stmt.SetZeroBlob("$contents", m.f.Size())
	if _, err := stmt.Step(); err != nil {
		return err
	}
	honeypotID := conn.LastInsertRowID()

	b, err := conn.OpenBlob("", "Honeypot", "Contents", honeypotID, true)
	if err != nil {
		return err
	}
	_, err = io.Copy(b, m.f)
	if closeErr := b.Close(); err == nil {
		err = closeErr
	}
	return err
}
