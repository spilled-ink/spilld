package smtpserver

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ErrServerClosed is returned by Serve when the Shutdown method is called.
var ErrServerClosed = errors.New("smtpd: Server closed")

// ErrTempFailure451 can be returned by the Msg Close method to report
// a temporary failure to the SMTP client.
var ErrTempFailure451 = errors.New("smtpd: Temporary failure ")

type Msg interface {
	AddRecipient(addr []byte) (bool, error)
	Write(line []byte) error
	Cancel()
	Close() error
}

type NewMessageFunc func(remoteAddr net.Addr, from []byte, authToken uint64) (Msg, error)

// Server is an SMTP server.
// Callers must provide a NewMessage function to process messages.
type Server struct {
	NewMessage    NewMessageFunc
	Hostname      string
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	MaxSize       int // maximum message bytes, default: 1 << 26
	MaxRecipients int // max message recipients (spec requires a min 100), default: 100
	MaxSessions   int // max concurrent clients, default: 8
	Rand          *rand.Rand
	TLSConfig     *tls.Config
	Logf          func(format string, v ...interface{})

	// AllowNoTLS set to true means a non-TLS SMTP session
	// can send mail without calling STARTTLS.
	// https://twitter.com/infinite_scream
	AllowNoTLS bool

	// Auth implements SMTP AUTH PLAIN.
	//
	// If a call to Auth is successful then it returns a non-zero
	// authToken that will be passed to NewMessage.
	Auth func(identity, user, pass []byte, remoteAddr string) (authToken uint64)

	// MustAuth specifies whether clients must use AUTH before sending.
	//
	// If MustAuth is false then it is up to NewMessage to avoid this
	// server becoming an open relay.
	MustAuth bool

	servingTLS bool

	randLock sync.Mutex // used after initialization to access Rand

	ln net.Listener

	shutdown         chan struct{}
	shutdownCtx      context.Context // nil until shutdown is closed
	shutdownComplete chan struct{}

	sessionsMu   sync.Mutex
	sessionsCond *sync.Cond
	sessions     map[*session]struct{}
}

func (server *Server) Shutdown(ctx context.Context) error {
	server.shutdownCtx = ctx
	close(server.shutdown)
	server.ln.Close()

	select {
	case <-server.shutdownComplete:
	case <-ctx.Done():
	}

	return nil
}

func (server *Server) ServeTLS(ln net.Listener) error {
	server.servingTLS = true
	return server.serve(ln)
}

func (server *Server) ServeSTARTTLS(ln net.Listener) error {
	return server.serve(ln)
}

func (server *Server) serve(ln net.Listener) error {
	if server.MaxSize == 0 {
		server.MaxSize = 1 << 26
	}
	if server.MaxRecipients == 0 {
		server.MaxRecipients = 100
	}
	if server.MaxSessions == 0 {
		server.MaxSessions = 8
	}
	if server.Rand == nil {
		server.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if server.Logf == nil {
		server.Logf = log.Printf
	}

	server.sessionsMu.Lock()
	server.sessionsCond = sync.NewCond(&server.sessionsMu)
	server.sessions = make(map[*session]struct{})
	server.sessionsMu.Unlock()

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
				server.Logf("smtpserver: accept error: %v", err)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		if server.servingTLS {
			c = tls.Server(c, server.TLSConfig)
		}
		tempDelay = 0
		go server.serveSession(c)
	}

	// Cleanup
	for {
		select {
		case <-server.shutdownCtx.Done():
			server.sessionsMu.Lock()
			for s := range server.sessions {
				s.c.Close()
			}
			server.sessionsMu.Unlock()

			return ErrServerClosed
		default:
			// Check on sessions
			server.sessionsMu.Lock()
			numSessions := len(server.sessions)
			server.sessionsMu.Unlock()

			if numSessions == 0 {
				return ErrServerClosed
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (server *Server) newID() int64 {
	for {
		server.randLock.Lock()
		id := server.Rand.Int63()
		server.randLock.Unlock()
		if id > 1 {
			return id
		}
	}
}

func (server *Server) serveSession(c net.Conn) {
	s := &session{
		server:     server,
		c:          c,
		br:         bufio.NewReader(c),
		bw:         bufio.NewWriter(c),
		id:         server.newID(),
		tls:        server.servingTLS,
		remoteAddr: c.RemoteAddr().String(),
	}
	if server.TLSConfig != nil {
		s.tlsConfig.InsecureSkipVerify = server.TLSConfig.InsecureSkipVerify
		s.tlsConfig.Certificates = append([]tls.Certificate{}, server.TLSConfig.Certificates...)
	}
	s.tlsConfig.GetConfigForClient = s.getConfigForClient

	server.sessionsMu.Lock()
	for len(server.sessions) > server.MaxSessions {
		server.sessionsCond.Wait()
	}
	server.sessions[s] = struct{}{}
	server.sessionsMu.Unlock()

	s.serve()
}

type session struct {
	server     *Server
	c          net.Conn
	br         *bufio.Reader
	bw         *bufio.Writer
	id         int64
	tlsConfig  tls.Config
	tls        bool
	numRcpts   int
	msg        Msg
	authToken  uint64
	remoteAddr string
}

// TODO: outlook needs TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384
// https://github.com/golang/go/issues/21633

func (s *session) getConfigForClient(info *tls.ClientHelloInfo) (*tls.Config, error) {
	s.log("STARTTLS client cipher suites", logs{"ciphers": info.CipherSuites})
	return &s.tlsConfig, nil
}

type logs map[string]interface{}

func (s *session) log(desc string, logFields logs) {
	now := time.Now().UnixNano()
	values, err := json.Marshal(logFields)
	if err != nil {
		values = []byte(err.Error())
	}
	s.server.Logf(`SMTP:{ "desc": %q, "remoteaddr": %q, "sessionid": %d, "time": %d, "tls": %v, "values": %s }`, desc, s.remoteAddr, s.id, now, s.tls, values)
}

func (s *session) serve() {
	defer func() {
		s.server.sessionsMu.Lock()
		delete(s.server.sessions, s)
		s.server.sessionsCond.Signal()
		s.server.sessionsMu.Unlock()
		if r := recover(); r != nil {
			s.log("panic", logs{"panic": r, "stack": string(debug.Stack())})
			panic(r)
		}
	}()
	defer func() {
		s.c.Close()
		if s.msg != nil {
			s.msg.Cancel()
			s.msg = nil
		}
	}()

	res := new(bytes.Buffer)

	fmt.Fprintf(s.bw, "220 %s ESMTP smsmtpd\r\n", s.server.Hostname)
	s.bw.Flush()
	for {
		if s.server.ReadTimeout != 0 {
			s.c.SetReadDeadline(time.Now().Add(s.server.ReadTimeout))
		}
		sl, err := s.br.ReadSlice('\n')
		if err != nil {
			s.log("command read error", logs{"err": err.Error()})
			return
		}
		if len(sl) < 3 || sl[len(sl)-2] != '\r' || sl[len(sl)-1] != '\n' {
			s.log("command does not end in CR-LR", logs{"cmd": string(sl)})
			fmt.Fprint(s.bw, "command does not end in CR-LF \r\n")
			s.bw.Flush()
			continue
		}
		var verbBytes, arg []byte
		if i := bytes.IndexByte(sl, ' '); i >= 0 {
			verbBytes = sl[:i]
			arg = bytes.TrimRightFunc(sl[i+1:len(sl)-2], unicode.IsSpace)
		} else {
			verbBytes = sl[:len(sl)-2]
		}

		verb := verbStr[string(verbBytes)]
		if verb == "" {
			s.log("unknown command", logs{"verb_raw": string(verbBytes)})
			fmt.Fprint(s.bw, "unknown command\r\n")
			s.bw.Flush()
			continue
		}

		res.Reset()
		moreSession := s.serveCmd(verb, arg, res)

		if res.Len() > 0 {
			s.bw.Write(res.Bytes())
			s.bw.Flush()
		}

		l := logs{
			"response":         string(res.Bytes()),
			"continue_session": bool(moreSession),
		}
		if len(arg) != 0 {
			l["arg"] = string(arg)
		}
		s.log(verb, l)

		if !moreSession {
			return
		}
	}
}

type moreSession bool

const (
	sessionContinue = moreSession(true)
	sessionEnd      = moreSession(false)
)

var fromRE = regexp.MustCompile(`[Ff][Rr][Oo][Mm]:<(.*)>`)
var rcptRE = regexp.MustCompile(`[Tt][Oo]:<(.*)>`)
var dotCRLF = []byte(".\r\n")

func (s *session) serveCmd(verb string, arg []byte, res io.Writer) moreSession {
	switch verb {
	case "NOOP":
		fmt.Fprintf(res, "250 2.0.0 OK\r\n")

	case "QUIT":
		if !s.hasNoArg(arg, res) {
			return sessionContinue
		}
		fmt.Fprintf(res, "221 2.0.0 Bye\r\n")
		return sessionEnd

	case "HELO", "EHLO":
		if !s.server.AllowNoTLS && !s.tls {
			fmt.Fprintf(res, "250-%s good morrow, TLS required\r\n", s.server.Hostname)
			fmt.Fprintf(res, "250 STARTTLS\r\n")
			return sessionContinue
		}
		fmt.Fprintf(res, "250-%s welcome!\r\n", s.server.Hostname)
		if !s.tls {
			fmt.Fprintf(res, "250-STARTTLS\r\n")
		}
		if s.server.Auth != nil {
			fmt.Fprintf(res, "250-AUTH PLAIN LOGIN\r\n")
		}
		fmt.Fprintf(res, "250-SIZE %d\r\n", s.server.MaxSize)
		fmt.Fprintf(res, "250-8BITMIME\r\n")
		fmt.Fprintf(res, "250-ENHANCEDSTATUSCODES\r\n")
		fmt.Fprintf(res, "250 SMTPUTF8\r\n")
		// TODO: DNS, PIPELINING, CHUNKING ???

	case "STARTTLS":
		// RFC 3207
		if s.tls {
			fmt.Fprintf(res, "454 TLS already in use\r\n")
			return sessionContinue
		}
		if !s.hasNoArg(arg, res) {
			return sessionContinue
		}
		fmt.Fprintf(s.bw, "220 Ready to start TLS\r\n")
		s.bw.Flush()

		s.c = tls.Server(s.c, &s.tlsConfig)
		s.br = bufio.NewReader(s.c)
		s.bw = bufio.NewWriter(s.c)
		s.tls = true

	case "AUTH":
		if s.server.Auth == nil {
			fmt.Fprintf(res, "451 authentication not supported\r\n") // TODO: determine correct error code
			return sessionContinue
		}

		var identity, user, pass []byte

		switch {
		case strings.HasPrefix(string(arg), "PLAIN"):
			identity, user, pass = s.serveAuthPlain(arg, res)
		case string(arg) == "LOGIN":
			user, pass = s.serveAuthLogin(res)
		default:
			fmt.Fprintf(res, "504 Unrecognized authentication type.\r\n")
			return sessionContinue
		}

		s.authToken = s.server.Auth(identity, user, pass, s.remoteAddr)
		if s.authToken == 0 {
			fmt.Fprintf(res, "535 5.7.1 authentication failed\r\n")
			return sessionContinue
		}
		fmt.Fprintf(res, "235 Authentication successful.\r\n")

	case "MAIL":
		if !s.hasTLS(res) {
			return sessionContinue
		}
		if s.server.MustAuth && s.authToken == 0 {
			fmt.Fprintf(res, "530 5.7.1 Authorization required\r\n")
			return sessionContinue
		}
		if s.msg != nil {
			fmt.Fprintf(res, "503 5.5.1 Error: MAIL command already called\r\n")
			return sessionContinue
		}
		select {
		case <-s.server.shutdown:
			fmt.Fprintf(res, "451 denied, server shutting down\r\n") // TODO: determine correct error code
			return sessionEnd
		default:
		}
		m := fromRE.FindSubmatch(arg)
		if m == nil {
			fmt.Fprintf(res, "501 5.1.7 Syntax error (bad sender address)\r\n")
			return sessionContinue
		}
		from := bytes.TrimSpace(m[1])
		if len(from) == 0 {
			fmt.Fprintf(res, "501 5.1.0 empty sender address\r\n")
			return sessionContinue
		}
		if bytes.IndexByte(from, '@') < 1 {
			fmt.Fprintf(res, "501 5.1.0 invalid sender address\r\n")
			return sessionContinue
		}
		var err error
		s.msg, err = s.server.NewMessage(s.c.RemoteAddr(), from, s.authToken)
		if err != nil {
			s.log("NewMessage failed", logs{"err": err.Error()})
			fmt.Fprintf(res, "451 denied\r\n")
			return sessionEnd
		}
		fmt.Fprintf(res, "250 2.1.0 OK\r\n")

	case "RCPT":
		if !s.hasTLS(res) {
			return sessionContinue
		}
		if s.msg == nil {
			fmt.Fprintf(res, "503 5.5.1 Error: MAIL command not called\r\n")
			return sessionContinue
		}
		if s.numRcpts+1 >= s.server.MaxRecipients {
			fmt.Fprintf(res, "452 Too many recipients\r\n")
			return sessionContinue
		}
		s.numRcpts++
		m := rcptRE.FindSubmatch(arg)
		if m == nil {
			fmt.Fprintf(res, "501 5.1.7 Syntax error (bad rcpt)\r\n")
			return sessionContinue
		}
		to := bytes.TrimSpace(m[1])
		if len(to) == 0 {
			fmt.Fprintf(res, "501 5.1.0 empty recipient address\r\n")
			return sessionContinue
		}
		if bytes.IndexByte(to, '@') < 1 {
			fmt.Fprintf(res, "501 5.1.0 invalid recipient address\r\n")
			return sessionContinue
		}
		if added, err := s.msg.AddRecipient(to); err != nil {
			s.log("AddRecipient failed", logs{"err": err.Error()})
			fmt.Fprintf(res, "550 Error: bad recipient, error processing\r\n")
			return sessionEnd
		} else if !added {
			fmt.Fprintf(res, "550 Error: bad recipient\r\n")
		} else {
			fmt.Fprintf(res, "250 2.1.0 OK\r\n")
		}

	case "DATA":
		if !s.hasTLS(res) || !s.hasNoArg(arg, res) {
			return sessionContinue
		}
		if s.msg == nil || s.numRcpts == 0 {
			fmt.Fprint(res, "503 5.5.1 Error: RCPT command not called\r\n")
			return sessionContinue
		}
		fmt.Fprint(s.bw, "354 Go ahead\r\n")
		s.bw.Flush()
		var n int
		for {
			/*if s.server.ReadTimeout != 0 {
				s.c.SetReadDeadline(time.Now().Add(s.server.ReadTimeout))
			}*/
			sl, err := s.br.ReadSlice('\n')
			if err != nil {
				return sessionEnd
			}
			if bytes.Equal(sl, dotCRLF) {
				break
			}
			if sl[0] == '.' {
				sl = sl[1:]
			}
			n += len(sl)
			if n > s.server.MaxSize {
				fmt.Fprint(res, "552 Too much mail data.\r\n")
				return sessionEnd
			}
			if err := s.msg.Write(sl); err != nil {
				fmt.Fprint(res, "550 Write error\r\n")
				return sessionEnd
			}
		}
		err := s.msg.Close()
		s.msg = nil
		s.numRcpts = 0
		if err != nil {
			if err == ErrTempFailure451 {
				fmt.Fprint(res, "451 Temporary failure, please try again later.\r\n")
			} else {
				fmt.Fprintf(res, "550 Write error: %v\r\n", err)
			}
			return sessionEnd
		}
		fmt.Fprint(res, "250 2.0.0 OK: queued\r\n")

	case "RSET":
		if !s.hasTLS(res) || !s.hasNoArg(arg, res) {
			return sessionContinue
		}
		if s.msg != nil {
			s.msg.Cancel()
		}
		s.msg = nil
		s.numRcpts = 0

	default:
		fmt.Fprintf(res, "502 5.5.2 Error: command not recognized\r\n")
	}

	return sessionContinue
}

func (s *session) serveAuthPlain(arg []byte, res io.Writer) (identity, user, pass []byte) {
	arg = arg[len("PLAIN "):]
	if len(arg) == 0 {
		io.WriteString(s.bw, "334 \r\n")
		s.bw.Flush()
		var err error
		arg, err = s.br.ReadSlice('\n')
		if err != nil {
			s.log("AUTH argument read error", logs{"err": err.Error()})
			return nil, nil, nil
		}
		if len(arg) < 3 || arg[len(arg)-2] != '\r' || arg[len(arg)-1] != '\n' {
			fmt.Fprint(res, "535 bad AUTH arg does not end in CRLF\r\n")
			return nil, nil, nil
		}
		arg = bytes.TrimSpace(arg)
	}

	auth := make([]byte, base64.StdEncoding.DecodedLen(len(arg)))
	if n, err := base64.StdEncoding.Decode(auth, arg); err != nil {
		fmt.Fprintf(res, "535 bad PLAIN base64 encoding\r\n") // TODO: find right error code
		return nil, nil, nil
	} else {
		auth = auth[:n]
	}
	i0 := bytes.IndexByte(auth, 0)
	if i0 == -1 {
		fmt.Fprintf(res, "535 invalid PLAIN data\r\n") // TODO: find right error code
		return nil, nil, nil
	}
	identity, auth = auth[:i0], auth[i0+1:]
	i0 = bytes.IndexByte(auth, 0)
	if i0 == -1 {
		fmt.Fprintf(res, "535 invalid PLAIN data\r\n") // TODO: find right error code
		return nil, nil, nil
	}
	user, pass = auth[:i0], auth[i0+1:]
	return identity, user, pass
}

func (s *session) serveAuthLogin(res io.Writer) (user, pass []byte) {
	// We do not advertise support for this, but we support
	// it in the hopes it will help our honeypot.
	io.WriteString(s.bw, "334 VXNlcm5hbWU6AA==\r\n") // base64 "Username:\x00"
	s.bw.Flush()
	arg, err := s.br.ReadSlice('\n')
	if err != nil {
		s.log("AUTH argument read error", logs{"err": err.Error()})
		return nil, nil
	}
	if len(arg) < 2 || arg[len(arg)-2] != '\r' || arg[len(arg)-1] != '\n' {
		fmt.Fprint(res, "535 bad AUTH arg does not end in CRLF\r\n")
		return nil, nil
	}
	arg = bytes.TrimSpace(arg)

	user = make([]byte, base64.StdEncoding.DecodedLen(len(arg)))
	if n, err := base64.StdEncoding.Decode(user, arg); err != nil {
		s.log("LOGIN bad user base64", logs{"arg": arg})
		fmt.Fprintf(res, "535 bad LOGIN user base64 encoding\r\n")
		return nil, nil
	} else {
		user = user[:n]
	}

	// Note that some clients, such as macOS Mail.app, require the \x00.
	io.WriteString(s.bw, "334 UGFzc3dvcmQ6AA==\r\n") // base64 "Password:\x00"
	s.bw.Flush()
	arg, err = s.br.ReadSlice('\n')
	if err != nil {
		s.log("AUTH argument read error", logs{"err": err.Error()})
		return nil, nil
	}
	if len(arg) < 2 || arg[len(arg)-2] != '\r' || arg[len(arg)-1] != '\n' {
		fmt.Fprint(res, "535 bad AUTH arg does not end in CRLF\r\n")
		return nil, nil
	}
	arg = bytes.TrimSpace(arg)

	pass = make([]byte, base64.StdEncoding.DecodedLen(len(arg)))
	if n, err := base64.StdEncoding.Decode(pass, arg); err != nil {
		// Safe to log. If it's invalid base64 it's not a pssword.
		s.log("LOGIN bad pass base64", logs{"arg": arg})
		fmt.Fprintf(res, "535 bad LOGIN pass base64 encoding\r\n")
		return nil, nil
	} else {
		pass = pass[:n]
	}

	return user, pass
}

func (s *session) hasNoArg(arg []byte, res io.Writer) bool {
	if len(arg) > 0 {
		fmt.Fprintf(res, "501 Syntax error (no parameters allowed)\r\n")
	}
	return len(arg) == 0
}

func (s *session) hasTLS(res io.Writer) bool {
	if s.server.AllowNoTLS || s.server.servingTLS {
		return true
	}
	if !s.tls {
		fmt.Fprint(res, "530 5.7.0 Must issue a STARTTLS command first\r\n")
	}
	return s.tls
}

var verbStr = map[string]string{
	"HELO":     "HELO",
	"EHLO":     "EHLO",
	"AUTH":     "AUTH",
	"QUIT":     "QUIT",
	"RSET":     "RSET",
	"NOOP":     "NOOP",
	"MAIL":     "MAIL",
	"RCPT":     "RCPT",
	"DATA":     "DATA",
	"STARTTLS": "STARTTLS",
}
