// Package imapserver implements an IMAP server as described in RFC 3501.
//
// To use this package, implement the DataStore interface, which is built
// on the Session and Mailbox interfaces defined in the imap package.
//
// Supported extension RFCs:
//	RFC 2177 IDLE
//	RFC 2971 ID
//	RFC 4315 UIDPLUS
// 	RFC 4731 ESEARCH
//	RFC 4978 COMPRESS=DEFLATE
//	RFC 5161 ENABLE
//	RFC 5258 LIST-EXTENDED
//	RFC 6154 SPECIAL-USE
//	RFC 7162 CONDSTORE
//
// TODO potential extension RFCs:
//	RFC 3516 BINARY (great extension, but not used by many clients)
//	RFC 4469 CATENATE
//	RFC 5256 SORT THREAD
//	RFC 6203 SEARCH=FUZZY
//	RFC 6855 UTF8=ACCEPT
//	RFC 7162 QRESYNC
//	RFC 7888 LITERAL-
//	RFC 7889 APPENDLIMIT
package imapserver

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"path"
	"runtime/debug"
	"runtime/trace"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"crawshaw.io/iox"
	"spilled.ink/imap"
	"spilled.ink/imap/imapparser"
	"spilled.ink/imap/imapparser/utf7mod"
)

var ErrServerClosed = errors.New("imapserver: Server closed")
var ErrBadCredentials = errors.New("imapserver: bad credentials")

type Server struct {
	Rand       io.Reader
	MaxConns   int
	TLSConfig  *tls.Config
	Filer      *iox.Filer
	Logf       func(format string, v ...interface{})
	DataStore  DataStore
	Debug      func(sessionID string) io.WriteCloser
	Version    string
	APNS       *APNS
	NotifyAPNS bool

	capabilities string

	ln net.Listener

	shutdown         chan struct{}
	shutdownCtx      context.Context
	shutdownComplete chan struct{}

	connsMu   sync.Mutex
	connsCond *sync.Cond
	conns     map[*Conn]struct{}
	users     map[int64]*user // connsMu guards map access, value contents independent
}

type DataStore interface {
	// Login authenticates a user and creates a session for them.
	//
	// Each Login call creates a separate session for a different Conn.
	//
	// The returned userID is, to imapserver, a unique opaque value
	// associated with a user. The username may change, but the userID
	// never does, and is used to associate sessions together.
	Login(c *Conn, username, password []byte) (userID int64, s imap.Session, err error)

	RegisterNotifier(imap.Notifier)
}

type user struct {
	mu     sync.Mutex
	userID int64
	conns  map[*Conn]struct{}
}

type notifier struct {
	server *Server
}

func (n *notifier) Notify(userID int64, mailboxID int64, mailboxName string, devices []imapparser.ApplePushDevice) {
	if n.server.APNS != nil && len(devices) > 0 {
		go n.server.APNS.Notify(devices)
	}
	user := n.server.getUser(userID)

	var update *idleUpdate

	user.mu.Lock()
	defer user.mu.Unlock()
	for c := range user.conns {
		c.bwMu.Lock()
		if c.mailbox != nil && c.mailbox.ID() == mailboxID && c.idleStarted {
			if update == nil {
				info, err := c.mailbox.Info()
				if err != nil {
					n.server.Logf("Notify Info failed: %v", err)
					return
				}
				update = &idleUpdate{
					typ:   idleTotalCount,
					value: info.NumMessages,
				}
			}
			c.updates = append(c.updates, *update)
			if c.idling {
				c.writeUpdates()
			}
		}
		c.bwMu.Unlock()
	}
}

func (server *Server) Shutdown(ctx context.Context) error {
	server.shutdownCtx = ctx
	close(server.shutdown)
	server.ln.Close()

	if server.APNS != nil {
		server.APNS.shutdown()
	}

	<-server.shutdownComplete

	return nil
}

func (server *Server) ServeTLS(ln net.Listener) error {
	if server.Rand == nil {
		server.Rand = rand.Reader
	}
	if server.MaxConns == 0 {
		server.MaxConns = 1 << 14
	}

	server.capabilities = capabilityAuth
	if server.APNS != nil {
		if err := server.APNS.start(); err != nil {
			return err
		}
		server.capabilities += " XAPPLEPUSHSERVICE"
	}

	server.DataStore.RegisterNotifier(&notifier{server: server})

	server.connsMu.Lock()
	server.connsCond = sync.NewCond(&server.connsMu)
	server.conns = make(map[*Conn]struct{})
	server.users = make(map[int64]*user)
	server.connsMu.Unlock()

	server.shutdown = make(chan struct{})
	server.shutdownComplete = make(chan struct{})
	server.ln = ln
	defer func() {
		ln.Close()
		close(server.shutdownComplete)
	}()

	var tempDelay time.Duration // sleep on accept failure

acceptLoop:
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-server.shutdown:
				break acceptLoop
			default:
			}
			if ne, _ := err.(net.Error); ne != nil && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				}
				tempDelay *= 2
				if tempDelay > 1*time.Second {
					tempDelay = 1 * time.Second
				}
				server.Logf("accept: %v", err)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		go server.serveSession(c)
	}

	// Cleanup
	for {
		select {
		case <-server.shutdownCtx.Done():
			server.connsMu.Lock()
			for c := range server.conns {
				c.close()
			}
			server.connsMu.Unlock()

			return ErrServerClosed
		default:
			// Check on connections
			server.connsMu.Lock()
			numSessions := len(server.conns)
			server.connsMu.Unlock()

			if numSessions == 0 {
				return ErrServerClosed
			}

			select {
			case <-server.shutdownCtx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func (server *Server) genSessionID() (string, error) {
	idb := make([]byte, 10)
	if _, err := io.ReadFull(server.Rand, idb); err != nil {
		return "", err
	}
	return base32.StdEncoding.EncodeToString(idb), nil
}

func (server *Server) getUser(userID int64) *user {
	server.connsMu.Lock()
	defer server.connsMu.Unlock()

	u := server.users[userID]
	if u == nil {
		u = &user{
			conns: make(map[*Conn]struct{}),
		}
		server.users[userID] = u
	}
	return u
}

func (server *Server) serveSession(netConn net.Conn) {
	sessionID, err := server.genSessionID()
	if err != nil {
		server.Logf("generating session ID failed: %v", err)
		netConn.Close()
		return
	}

	netConn = tls.Server(netConn, server.TLSConfig)
	c := &Conn{
		ID: sessionID,
		Logf: func(format string, v ...interface{}) {
			server.Logf("session("+sessionID+"): "+format, v...)
		},

		server:  server,
		netConn: netConn,
		br:      bufio.NewReader(netConn),
		bw:      bufio.NewWriter(netConn),
	}

	if server.Debug != nil {
		c.debugFile = server.Debug(sessionID)
		if c.debugFile != nil {
			c.debugW = newDebugWriter(sessionID, server.Logf, c.debugFile)
		}
	}
	c.initBufio(c.netConn, c.netConn)

	server.connsMu.Lock()
	for len(server.conns) > server.MaxConns {
		server.connsCond.Wait()
	}
	server.conns[c] = struct{}{}
	server.connsMu.Unlock()

	c.serve()
}

type Conn struct {
	Context context.Context
	ID      string
	Logf    func(format string, v ...interface{})

	userID    int64
	session   imap.Session
	mailbox   imap.Mailbox
	readOnly  bool
	condstore bool // client has send a CONDSTORE-related command

	debugFile io.WriteCloser
	debugW    *debugWriter

	server  *Server
	netConn net.Conn
	br      *bufio.Reader
	p       *imapparser.Parser

	bwMu          sync.Mutex
	bw            *bufio.Writer
	compressing   bool // COMPRESS active
	compressFlush func() error
	idleStarted   bool // c.mailbox.Idle has been called
	idling        bool // IDLE in progress
	updates       []idleUpdate
}

func (c *Conn) initBufio(r io.Reader, w io.Writer) {
	if c.debugFile == nil {
		c.br = bufio.NewReader(r)
		c.bw = bufio.NewWriter(w)
	} else {
		c.br = bufio.NewReader(io.TeeReader(r, c.debugW.client))
		c.bw = bufio.NewWriter(io.MultiWriter(c.debugW.server, w))
	}
	if c.p != nil {
		c.p.Scanner.SetSource(c.br)
	}
}

func (c *Conn) flush() error {
	if err := c.bw.Flush(); err != nil {
		return err
	}
	if c.compressFlush != nil {
		if err := c.compressFlush(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Conn) writef(format string, v ...interface{}) {
	fmt.Fprintf(c.bw, format, v...)
}

// "<s.p.Command.Tag> msg\r\n"
func (c *Conn) respondln(format string, v ...interface{}) {
	c.bw.Write(c.p.Command.Tag)
	c.bw.WriteByte(' ')
	fmt.Fprintf(c.bw, format, v...)
	c.bw.WriteByte('\r')
	c.bw.WriteByte('\n')
	if err := c.flush(); err != nil {
		c.close()
	}
}

func (c *Conn) close() {
	c.closeMailbox()
	if c.debugFile != nil {
		c.flush()
		io.CopyN(ioutil.Discard, c.br, int64(c.br.Buffered()))
		c.netConn.SetReadDeadline(time.Now())
		io.Copy(ioutil.Discard, c.br)
	}
	c.netConn.Close()
}

func (c *Conn) writeStringBytes(s []byte) {
	c.writeString(string(s))
}

func (c *Conn) writeString(s string) {
	if s == "" {
		c.writef(`""`)
		return
	}

	type strType int

	const (
		strLiteral strType = iota
		strQuote
		strAtom
	)

	strTypeVal := strAtom
	sCheck := s
	for len(sCheck) > 0 {
		r, sz := utf8.DecodeRuneInString(sCheck)
		sCheck = sCheck[sz:]
		if r == utf8.RuneError || r == '\r' || r == '\n' {
			strTypeVal = strLiteral
			break
		}
		if r == '"' {
			// TODO: is this necessary? is "\"" a valid quoted IMAP string?
			strTypeVal = strLiteral
			break
		}
		switch {
		case 'A' <= r && r <= 'Z',
			'a' <= r && r <= 'z',
			'0' <= r && r <= '9',
			r == '-', r == '_', r == '.':
			// easily-allowable in an atom
		default:
			strTypeVal = strQuote
		}
	}

	if strTypeVal == strAtom {
		c.bw.WriteString(s)
		return
	}

	b := make([]byte, 0, 128)
	b, err := utf7mod.AppendEncode(b, []byte(s))
	if err != nil {
		c.Logf("cannot encode string %q", s)
	}

	switch strTypeVal {
	case strLiteral:
		c.writef("{%d}\r\n", len(s))
		c.flush()
		if c.debugW != nil {
			c.debugW.server.literalDataFollows(len(s))
		}
		c.bw.Write(b)
	case strQuote:
		c.writef("%q", b)
	default:
		panic("invalid strTypeVal")
	}
}

func (c *Conn) writeLiteral(r io.Reader, n int64) {
	c.writef("{%d}\r\n", n)
	c.flush()
	if c.debugW != nil {
		c.debugW.server.literalDataFollows(int(n))
	}
	if n2, err := io.CopyN(c.bw, r, n); err != nil {
		c.Logf("writeLiteral(n=%d) failed: %v (n2=%d)", n, err, n2)
	}
}

func (c *Conn) writeUpdates() {
	// Remove out of date EXISTS messages.
	countCount := 0
	for _, update := range c.updates {
		if update.typ == idleTotalCount {
			countCount++
		}
	}
	if countCount > 1 {
		orig := c.updates
		c.updates = c.updates[:0]
		for _, update := range orig {
			if update.typ == idleTotalCount && countCount > 1 {
				countCount--
				continue
			}
			c.updates = append(c.updates, update)
		}
	}

	// Write out updates.
	for _, update := range c.updates {
		switch update.typ {
		case idleExpunge:
			c.writef("* %d EXPUNGE\r\n", update.value)
		case idleTotalCount:
			c.writef("* %d EXISTS\r\n", update.value)
		}
	}
	if len(c.updates) > 0 {
		c.flush()
		c.updates = c.updates[:0]
	}
}

func (srcConn *Conn) sendIdleUpdate(mailboxID int64, update idleUpdate) {
	srcConn.server.connsMu.Lock()
	user := srcConn.server.users[srcConn.userID]
	srcConn.server.connsMu.Unlock()

	user.mu.Lock()
	defer user.mu.Unlock()
	for c := range user.conns {
		if srcConn == c {
			// already holding lock
			if !update.skipSelf && c.mailbox != nil && c.mailbox.ID() == mailboxID && c.idleStarted {
				c.updates = append(c.updates, update)
			}
			continue
		}

		c.bwMu.Lock()
		if c.mailbox != nil && c.mailbox.ID() == mailboxID && c.idleStarted {
			c.updates = append(c.updates, update)
			if c.idling {
				// TODO: if we are going to do this here while holding
				// user.mu, we need to be sure the write has a reasonable timeout.
				c.writeUpdates()
			}
		}
		c.bwMu.Unlock()
	}
}

type idleUpdateType int

const (
	idleTotalCount idleUpdateType = iota + 1
	idleExpunge
)

// idleUpdate is a change in the Mailbox state.
type idleUpdate struct {
	typ      idleUpdateType
	value    uint32
	skipSelf bool
}

func (c *Conn) serve() {
	ctx, cancel := context.WithCancel(context.Background())
	ctx, task := trace.NewTask(ctx, "imap-session")
	c.Context = ctx

	defer func() {
		c.closeMailbox()
		if c.session != nil {
			c.session.Close()
		}

		task.End()
		cancel()

		c.close()
		if c.debugFile != nil {
			if err := c.debugFile.Close(); err != nil {
				c.Logf("%v", err)
			}
		}

		c.server.connsMu.Lock()
		delete(c.server.conns, c)
		if c.userID != 0 {
			u := c.server.users[c.userID]
			u.mu.Lock()
			delete(u.conns, c)
			u.mu.Unlock()
		}
		c.server.connsCond.Signal()
		c.server.connsMu.Unlock()

		if r := recover(); r != nil {
			c.Logf("panic: %s", string(debug.Stack()))
			panic(r)
		}
	}()
	litf := c.server.Filer.BufferFile(0)
	defer litf.Close()

	c.bwMu.Lock()
	c.writef("* OK IMAP4 spilled.ink ready\r\n")
	if err := c.flush(); err != nil {
		c.close()
	}
	c.bwMu.Unlock()

	contFn := func(msg string, len uint32) {
		c.bwMu.Lock()
		defer c.bwMu.Unlock()
		c.writef(msg)
		c.flush()

		if c.debugW != nil {
			c.debugW.client.literalDataFollows(int(len))
		}
	}

	c.p = &imapparser.Parser{
		Scanner: imapparser.NewScanner(c.br, litf, contFn),
	}

	for {
		c.br.Peek(1) // block until the client sends something
		if !c.serveParseCmd() {
			break
		}
	}
}

const (
	capability     = `IMAP4rev1 AUTH=PLAIN ENABLE ID`
	capabilityAuth = `IMAP4rev1 COMPRESS=DEFLATE CONDSTORE ENABLE ` +
		`ESEARCH ID IDLE LIST-EXTENDED MOVE SPECIAL-USE UIDPLUS`
)

func (c *Conn) serveParseCmd() bool {
	origCtx := c.Context
	ctx, task := trace.NewTask(c.Context, "imap-request")
	c.Context = ctx
	defer func() {
		task.End()
		c.Context = origCtx
	}()

	trace.Log(c.Context, "session-id", c.ID)

	if err := c.p.ParseCommand(); err == io.EOF {
		return false
	} else if ne, _ := err.(net.Error); ne != nil {
		return false
	} else if te, isTagged := err.(imapparser.TaggedError); isTagged {
		c.bwMu.Lock()
		fmt.Fprintf(c.bw, "%s BAD %v\r\n", te.Tag, te.Err)
		c.flush()
		c.bwMu.Unlock()
		return true
	} else if _, isParseError := err.(imapparser.ParseError); isParseError {
		c.bwMu.Lock()
		c.Logf("parse error: %v", err)
		trace.Logf(c.Context, "parse_error", "%v", err)
		fmt.Fprintf(c.bw, "* BAD %v\r\n", err)
		c.flush()
		c.bwMu.Unlock()
		return true
	} else if err != nil {
		c.bwMu.Lock()
		c.Logf("conn error: %v", err)
		trace.Logf(c.Context, "conn_error", "%v", err)
		fmt.Fprintf(c.bw, "* BAD connection error\r\n")
		c.flush()
		c.bwMu.Unlock()
		return false
	}
	trace.Logf(c.Context, "imap-request-cmd", "%v", c.p.Command)
	// TODO: for long-lived connections we want a very long (possibly infinite)
	//       read deadline. However we could (and should?) have a short write deadline.
	c.serveCmd()
	return true
}

func (c *Conn) serveCmd() {
	c.bwMu.Lock()
	defer c.bwMu.Unlock()

	c.writeUpdates()

	cmd := &c.p.Command
	switch cmd.Name {
	case "CAPABILITY":
		if c.p.Mode == imapparser.ModeNonAuth {
			c.writef("* CAPABILITY %s\r\n", capability)
		} else {
			c.writef("* CAPABILITY %s\r\n", c.server.capabilities)
		}
		c.respondln("OK Completed")

	case "COMPRESS":
		if c.compressing {
			c.respondln("NO [COMPRESSIONACTIVE] DEFLATE active")
			return
		}
		c.compressing = true

		c.respondln("OK DEFLATE active")
		r := flate.NewReader(c.netConn)
		w, _ := flate.NewWriter(c.netConn, 1)
		c.compressFlush = w.Flush
		c.initBufio(r, w)

	case "LOGOUT":
		c.writef("* BYE\r\n%s OK Completed\r\n", cmd.Tag)
		c.flush()
		c.close()

	case "NOOP":
		c.respondln("OK nothing offered, nothing given")

	case "LOGIN", "AUTHENTICATE":
		if c.p.Mode != imapparser.ModeNonAuth {
			c.respondln("BAD wrong mode")
			return
		}
		userID, session, err := c.server.DataStore.Login(c, cmd.Auth.Username, cmd.Auth.Password)
		if err == ErrBadCredentials {
			c.respondln("NO bad credenttials")
			return
		} else if err != nil {
			c.respondln("BAD %v", err)
			return
		}
		trace.Logf(c.Context, "username", "%s", cmd.Auth.Username)
		c.p.Mode = imapparser.ModeAuth
		c.userID = userID
		c.session = session

		u := c.server.getUser(userID)

		u.mu.Lock()
		u.conns[c] = struct{}{}
		u.mu.Unlock()

		c.respondln("OK [CAPABILITY %s] logged in", c.server.capabilities)

	case "STARTTLS":
		c.respondln("BAD already using TLS")
	case "APPEND":
		c.cmdAppend()
	case "CREATE":
		// TODO AttrListFlag
		if err := c.session.CreateMailbox(c.p.Command.Mailbox, 0); err != nil {
			c.respondln("NO DELETE failed %v", err)
		} else {
			c.respondln("OK DELETE completed")
		}
	case "DELETE":
		if err := c.session.DeleteMailbox(c.p.Command.Mailbox); err != nil {
			c.respondln("NO DELETE failed %v", err)
		} else {
			c.respondln("OK DELETE completed")
		}
	case "ENABLE":
		c.respondln("OK completed")
	case "EXAMINE":
		c.cmdSelect()
	case "ID":
		buf := new(bytes.Buffer)
		for i, param := range c.p.Command.Params {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(buf, "%s", param)
		}
		c.Logf("client-id: [%s]", buf.String())
		c.writef(`* ID ("name" "spilld" "vendor" "Spilled Ink"`)
		c.writef(` "support-url" "https://github.com/spilledink"`)
		c.writef(` "version" %q`, c.server.Version)
		c.writef(")\r\n")
		c.respondln("OK success")
	case "IDLE":
		c.idleStarted = true
		c.idling = true
		if c.mailbox != nil {
			c.writeUpdates()
		}

		c.bwMu.Unlock()
		sl, err := c.br.ReadSlice('\n')
		c.bwMu.Lock()

		if err != nil {
			c.respondln("BAD IDLE terminated: %v", err)
		} else {
			if strings.EqualFold(string(sl), "DONE\r\n") {
				c.respondln("OK IDLE terminated")
			} else {
				c.respondln("BAD IDLE terminated: unrecognized response: %q", string(sl))
			}
		}

		c.idling = false
	case "LIST", "LSUB":
		c.cmdList()
	case "RENAME":
		old, new := c.p.Command.Rename.OldMailbox, c.p.Command.Rename.NewMailbox
		if err := c.session.RenameMailbox(old, new); err != nil {
			c.respondln("NO RENAME %v", err)
		} else {
			c.respondln("OK RENAME completed")
		}
	case "SELECT":
		c.cmdSelect()
	case "STATUS":
		c.cmdStatus()
	case "SUBSCRIBE":
		c.respondln("BAD SUBSCRIBE TODO")
	case "UNSUBSCRIBE":
		c.respondln("BAD UNSUBSCRIBE TODO")
	case "CHECK":
		c.respondln("OK CHECK completed")
	case "CLOSE":
		totalCountChanged := false
		fn := func(seqNum uint32) {
			c.sendIdleUpdate(c.mailbox.ID(), idleUpdate{
				typ:      idleExpunge,
				value:    seqNum,
				skipSelf: true,
			})
			totalCountChanged = true
		}
		if err := c.mailbox.Expunge(nil, fn); err != nil {
			c.writef("* BAD CLOSE server expunge error: %v\r\n", err)
		} else if totalCountChanged {
			if info, err := c.mailbox.Info(); err == nil {
				c.sendIdleUpdate(c.mailbox.ID(), idleUpdate{
					typ:      idleTotalCount,
					value:    info.NumMessages,
					skipSelf: true,
				})
			}
		}
		c.closeMailbox()
		c.respondln("OK CLOSE completed, returned to authenticated state.")
	case "EXPUNGE":
		c.cmdExpunge()
	case "COPY", "MOVE":
		c.cmdCopyOrMove()
	case "FETCH":
		c.cmdFetch()
	case "STORE":
		c.cmdStore()
	case "SEARCH":
		c.cmdSearch()
	case "XAPPLEPUSHSERVICE":
		c.cmdXApplePushService()
	}
}

func (c *Conn) closeMailbox() {
	if c.mailbox == nil {
		return
	}
	if err := c.mailbox.Close(); err != nil {
		c.writef("* BAD CLOSE server error: %v\r\n", err)
	}
	c.readOnly = false
	c.mailbox = nil
	c.p.Mode = imapparser.ModeAuth
	c.updates = c.updates[:0]
	c.idling = false
	c.idleStarted = false
}

func (c *Conn) cmdAppend() {
	cmd := &c.p.Command

	mailbox, err := c.session.Mailbox(cmd.Mailbox)
	if err != nil {
		c.respondln("NO APPEND %v", err)
		return
	}
	if mailbox == nil {
		c.respondln("NO APPEND no such mailbox")
		return
	}
	info, err := mailbox.Info()
	if err != nil {
		c.respondln("NO APPEND info %v", err)
		return
	}

	var date time.Time
	if len(cmd.Append.Date) > 0 {
		var err error
		date, err = time.Parse("02-Jan-2006 15:04:05 -0700", string(cmd.Append.Date))
		if err != nil {
			c.respondln("NO APPEND bad date %v", err)
			return
		}
	}

	uid, err := mailbox.Append(cmd.Append.Flags, date, cmd.Literal)
	if err != nil {
		c.respondln("NO APPEND %v", err)
		return
	}
	if info, err := mailbox.Info(); err == nil {
		c.sendIdleUpdate(mailbox.ID(), idleUpdate{
			typ:   idleTotalCount,
			value: info.NumMessages,
		})
	}

	c.writeUpdates()
	// APPENDUID is defined in RFC 4315.
	c.respondln("OK [APPENDUID %d %d] APPEND completed", info.UIDValidity, uid)
}

func (c *Conn) cmdExpunge() {
	var uidSeqs []imapparser.SeqRange
	if c.p.Command.UID {
		uidSeqs = c.p.Command.Sequences
	}
	err := c.mailbox.Expunge(uidSeqs, func(seqNum uint32) {
		c.sendIdleUpdate(c.mailbox.ID(), idleUpdate{
			typ:      idleExpunge,
			value:    seqNum,
			skipSelf: true,
		})
		c.writef("* %d EXPUNGE\r\n", seqNum)
	})
	if err != nil {
		c.respondln("NO EXPUNGE %v", err)
		return
	}
	if info, err := c.mailbox.Info(); err == nil {
		c.sendIdleUpdate(c.mailbox.ID(), idleUpdate{
			typ:   idleTotalCount,
			value: info.NumMessages,
		})
	}
	//c.writeUpdates()
	c.respondln("OK EXPUNGE completed")
}

func (c *Conn) cmdList() {
	cmd := &c.p.Command
	if len(cmd.List.ReferenceName) == 0 && len(cmd.List.MailboxGlob) == 0 {
		c.writef(`* %s (\Noselect) "/" ""`+"\r\n", cmd.Name)
		c.respondln("OK Success")
		return
	}
	if len(cmd.List.ReferenceName) > 0 || string(cmd.List.MailboxGlob) != "*" {
		c.respondln("BAD Not yet implemented")
		return
	}
	if len(cmd.List.SelectOptions) > 0 {
		c.respondln("BAD LIST select options not implemented")
		return
	}
	if len(cmd.List.ReturnOptions) > 0 {
		if len(cmd.List.ReturnOptions) == 1 && cmd.List.ReturnOptions[0] == "SPECIAL-USE" {
			// return as normal, we include SPECIAL-USE flags by default
		} else {
			c.respondln("BAD LIST return options not implemented")
			return
		}
	}

	list, err := c.session.Mailboxes()
	if err != nil {
		c.respondln("BAD %s %v", cmd.Name, err)
		return
	}
	hasKids := make(map[string]bool)
	for _, s := range list {
		hasKids[path.Dir(s.Name)] = true
	}

	for _, s := range list {
		kidFlag := `\HasNoChildren` // RFC 3348 child mailbox extension
		if hasKids[s.Name] {
			kidFlag = `\HasChildren`
		}
		if cmd.Name == "LSUB" {
			kidFlag = ""
		}
		extAttr := s.Attrs.String()
		spacer := ""
		if extAttr != "" {
			spacer = " "
		}
		c.writef("* %s (%s%s%s) \"/\" ", cmd.Name, kidFlag, spacer, extAttr)
		c.writeString(s.Name)
		c.writef("\r\n")
	}
	c.respondln("OK Success")
}

func (c *Conn) cmdSelect() {
	cmd := &c.p.Command

	c.closeMailbox()

	var err error
	c.readOnly = cmd.Name == "EXAMINE"
	c.mailbox, err = c.session.Mailbox(cmd.Mailbox)
	if err != nil {
		c.p.Mode = imapparser.ModeAuth
		c.respondln("NO %v", err)
		return
	}
	if c.mailbox == nil {
		c.p.Mode = imapparser.ModeAuth
		c.respondln("NO unknown mailbox")
		return
	}
	c.p.Mode = imapparser.ModeSelected

	info, err := c.mailbox.Info()
	if err != nil {
		c.mailbox = nil
		c.p.Mode = imapparser.ModeAuth
		c.respondln("NO SELECT internal error")
		c.Logf("SELECT: %v", err)
		return
	}

	c.writef("* %d EXISTS\r\n", info.NumMessages)
	c.writef("* %d RECENT\r\n", info.NumRecent)
	c.writef(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen)` + "\r\n")
	if c.readOnly {
		c.writef(`* OK [PERMANENTFLAGS ()] No permanent flags permitted` + "\r\n")
	} else {
		c.writef(`* OK [PERMANENTFLAGS (\Answered \Flagged \Draft \Deleted \Seen)] Ok` + "\r\n")
	}
	c.writef("* OK [HIGHESTMODSEQ %d]\r\n", info.HighestModSequence)
	if info.FirstUnseenSeqNum > 0 {
		c.writef("* OK [UNSEEN %d]\r\n", info.FirstUnseenSeqNum)
	}
	c.writef("* OK [UIDVALIDITY %d]\r\n", info.UIDValidity)
	c.writef("* OK [UIDNEXT %d]\r\n", info.UIDNext)

	if cmd.Condstore {
		c.condstore = true
	}
	store := ""
	if c.condstore {
		store = ", CONDSTORE enabled"
	}
	if c.readOnly {
		c.respondln("OK [READ-ONLY] EXAMINE completed%s", store)
	} else {
		c.respondln("OK [READ-WRITE] SELECT completed%s", store)
	}
}

func (c *Conn) cmdStatus() {
	cmd := &c.p.Command

	mailbox, err := c.session.Mailbox(cmd.Mailbox)
	if err != nil {
		c.respondln("BAD STATUS %v", err)
		return
	}
	info, err := mailbox.Info()
	if err != nil {
		c.respondln("BAD STATUS %v", err)
		return
	}

	c.writef("* STATUS ")
	c.writeStringBytes(cmd.Mailbox)
	c.writef(" (")

	for i, item := range cmd.Status.Items {
		if i > 0 {
			c.writef(" ")
		}
		switch item {
		case imapparser.StatusMessages:
			c.writef("MESSAGES %d", info.NumMessages)
		case imapparser.StatusRecent:
			c.writef("RECENT %d", info.NumRecent)
		case imapparser.StatusUIDNext:
			c.writef("UIDNEXT %d", info.UIDNext)
		case imapparser.StatusUIDValidity:
			c.writef("UIDVALIDITY %d", info.UIDValidity)
		case imapparser.StatusUnseen:
			c.writef("UNSEEN %d", info.NumUnseen)
		case imapparser.StatusHighestModSeq:
			c.writef("HIGHESTMODSEQ %d", info.HighestModSequence)
		default:
			c.Logf("STATUS: unknown item: %v", item)
		}
	}
	c.writef(")\r\n")
	c.respondln("OK STATUS complete")
}

func (c *Conn) cmdCopyOrMove() {
	cmd := &c.p.Command

	dst, err := c.session.Mailbox(cmd.Mailbox)
	if err != nil {
		c.respondln("BAD destination mailbox %v", err)
		return
	}
	dstInfo, err := dst.Info()
	if err != nil {
		c.respondln("BAD destination mailbox info %v", err)
		return
	}

	var srcUIDs, dstUIDs []imapparser.SeqRange
	var oldSeqNums []uint32

	if cmd.Name == "MOVE" {
		fn := func(srcSeqNum, srcUID, dstUID uint32) {
			oldSeqNums = append(oldSeqNums, srcSeqNum)
			srcUIDs = imapparser.AppendSeqRange(srcUIDs, srcUID)
			dstUIDs = imapparser.AppendSeqRange(dstUIDs, dstUID)
			c.sendIdleUpdate(c.mailbox.ID(), idleUpdate{
				typ:      idleExpunge,
				value:    srcSeqNum,
				skipSelf: true,
			})
		}
		if err := c.mailbox.Move(cmd.UID, cmd.Sequences, dst, fn); err != nil {
			c.respondln("BAD MOVE %v", err)
			return
		}
		if info, err := c.mailbox.Info(); err == nil {
			c.sendIdleUpdate(c.mailbox.ID(), idleUpdate{
				typ:   idleTotalCount,
				value: info.NumMessages,
			})
		}
		if info, err := dst.Info(); err == nil {
			c.sendIdleUpdate(dst.ID(), idleUpdate{
				typ:   idleTotalCount,
				value: info.NumMessages,
			})
		}
	} else {
		fn := func(srcUID, dstUID uint32) {
			srcUIDs = imapparser.AppendSeqRange(srcUIDs, srcUID)
			dstUIDs = imapparser.AppendSeqRange(dstUIDs, dstUID)
		}
		if err := c.mailbox.Copy(cmd.UID, cmd.Sequences, dst, fn); err != nil {
			c.respondln("BAD COPY %v", err)
			return
		}
		if info, err := dst.Info(); err == nil {
			c.sendIdleUpdate(dst.ID(), idleUpdate{
				typ:   idleTotalCount,
				value: info.NumMessages,
			})
		}
	}

	if len(srcUIDs) > 0 {
		c.writef("* OK [COPYUID %d ", dstInfo.UIDValidity)
		imapparser.FormatSeqs(c.bw, srcUIDs)
		c.writef(" ")
		imapparser.FormatSeqs(c.bw, dstUIDs)
		c.writef("]\r\n")
	}

	if cmd.Name == "MOVE" {
		for _, oldSeqNum := range oldSeqNums {
			c.writef("* %d EXPUNGE\r\n", oldSeqNum)
		}
		c.writeUpdates()
	}
	c.respondln("OK %s done", cmd.Name)
}

func (c *Conn) setCondStore() {
	if c.condstore {
		return
	}
	c.condstore = true
	modSeq, err := c.mailbox.HighestModSequence()
	if err != nil {
		c.Logf("STORE: failed to get HIGHESTMODSEQ: %v", err)
	} else {
		c.writef("* OK [HIGHESTMODSEQ %d]\r\n", modSeq)
	}
}

func (c *Conn) cmdStore() {
	cmd := &c.p.Command

	// TODO: if UnchangedSince == 0 but was set, always fail. Do in imapparser?

	res, err := c.mailbox.Store(cmd.UID, cmd.Sequences, &cmd.Store)
	if err != nil {
		c.respondln("NO STORE %v", err)
		return
	}

	if cmd.Store.UnchangedSince != 0 {
		c.setCondStore()
	}

	for _, stored := range res.Stored {
		if cmd.Store.UnchangedSince == 0 && cmd.Store.Silent {
			continue
		}
		c.writef("* %d FETCH (", stored.SeqNum)
		needSpace := false
		if cmd.UID {
			needSpace = true
			c.writef("UID %d", stored.UID)
		}
		if c.condstore {
			// Always return the MODSEQ value if we have entered CONDSTORE mode.
			// See RFC 7162 Section 3.1.4.2.
			if needSpace {
				c.writef(" ")
			}
			needSpace = true
			c.writef("MODSEQ (%d)", stored.ModSequence)
		}
		if !cmd.Store.Silent {
			if needSpace {
				c.writef(" ")
			}
			c.writef("FLAGS (")
			for i, flag := range stored.Flags {
				if i > 0 {
					c.writef(" ")
				}
				if flag != "" && flag[0] == '\\' {
					c.writef("%s", flag)
				} else {
					c.writeString(flag)
				}
			}
			c.writef(")")
		}
		c.writef(")\r\n")
	}

	modified := new(bytes.Buffer)
	if len(res.FailedModified) > 0 {
		modified.WriteString("[MODIFIED ")
		imapparser.FormatSeqs(modified, res.FailedModified)
		modified.WriteString("]")
	}
	if modified.Len() > 0 {
		c.respondln("OK %s Conditional STORE failed", modified.Bytes())
	} else if cmd.Store.UnchangedSince > 0 {
		c.respondln("OK Conditional STORE completed")
	} else {
		c.respondln("OK STORE completed")
	}
}

func hasModSeqOp(op *imapparser.SearchOp) bool {
	if op.Key == "MODSEQ" {
		return true
	}
	for _, ch := range op.Children {
		if hasModSeqOp(&ch) {
			return true
		}
	}
	return false
}

func (c *Conn) cmdSearch() {
	cmd := &c.p.Command

	var maxModSeq, minResultModSeq, maxResultModSeq int64
	var minResult, maxResult uint32 = math.MaxUint32, 0
	var results []uint32
	err := c.mailbox.Search(cmd.Search.Op, func(data imap.MessageSummary) {
		num := data.UID
		if !cmd.UID {
			num = data.SeqNum
		}
		results = append(results, num)
		if data.ModSeq > maxModSeq {
			maxModSeq = data.ModSeq
		}
		if num < minResult {
			minResult = num
			minResultModSeq = data.ModSeq
		}
		if num > maxResult {
			maxResult = num
			maxResultModSeq = data.ModSeq
		}
	})
	if err != nil {
		c.respondln("BAD SEARCH error: %v", err)
		return
	}
	if len(cmd.Search.Return) > 0 {
		c.writef("* ESEARCH (TAG %q)", cmd.Tag) // RFC 4731

		var min, max, count, all bool // write parameters in a fixed order
		for _, v := range cmd.Search.Return {
			switch v {
			case "MIN":
				min = true
			case "MAX":
				max = true
			case "COUNT":
				count = true
			case "ALL":
				all = true
			}
		}

		if count {
			c.writef(" COUNT %d", len(results))
		}
		if len(results) > 0 {
			if min {
				c.writef(" MIN %d", minResult)
			}
			if max {
				c.writef(" MAX %d", maxResult)
			}
			if all {
				var vals []imapparser.SeqRange
				for _, res := range results {
					vals = imapparser.AppendSeqRange(vals, res)
				}
				c.writef(" ALL ")
				imapparser.FormatSeqs(c.bw, vals)
			}
			if hasModSeqOp(cmd.Search.Op) {
				// RFC 4731 Section 3.2
				var modSeq int64
				if all || count {
					modSeq = maxModSeq
				} else if min && max {
					modSeq = minResultModSeq
					if maxResultModSeq > modSeq {
						modSeq = maxResultModSeq
					}
				} else if min {
					modSeq = minResultModSeq
				} else { // max
					modSeq = maxResultModSeq
				}
				c.writef(" MODSEQ %d", modSeq)
			}
		}
		c.writef("\r\n")
	} else if len(results) > 0 {
		c.writef("* SEARCH")
		for _, id := range results {
			c.writef(" %d", id)
		}
		if hasModSeqOp(cmd.Search.Op) {
			c.writef(" (MODSEQ %d)", maxModSeq)
		}
		c.writef("\r\n")
	}
	uidstr := ""
	if cmd.UID {
		uidstr = "UID "
	}
	c.respondln("OK %sSEARCH", uidstr)
}

func (c *Conn) cmdXApplePushService() {
	if c.server.APNS == nil {
		c.respondln("BAD XAPPLEPUSHSERVICE not supported\r\n")
		return
	}

	aps := c.p.Command.ApplePushService
	for _, mailbox := range aps.Mailboxes {
		if err := c.session.RegisterPushDevice(mailbox, aps.Device); err != nil {
			c.respondln("BAD XAPPLEPUSHSERVICE %v", err)
			return
		}
	}
	c.writef("* XAPPLEPUSHSERVICE aps-version \"2\" aps-topic %q\r\n", c.server.APNS.UID)
	c.respondln("OK XAPPLEPUSHSERVICE Registration success.")
}
