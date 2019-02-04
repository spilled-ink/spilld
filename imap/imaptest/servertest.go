package imaptest

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"crawshaw.io/iox"
	"spilled.ink/imap"
	"spilled.ink/imap/imapparser"
	"spilled.ink/imap/imapserver"
	"spilled.ink/tlstest"
)

type TestFn struct {
	Name string
	Fn   func(t *testing.T, server *TestServer)
}

var Tests = []TestFn{
	{"UIDExpunge", TestUIDExpunge},
	{"Flags", TestFlags},
	{"Append", TestAppend},
	{"Copy", TestCopy},
	{"Move", TestMove},
	{"Immutable", TestImmutable},
	{"FetchBody", TestFetchBody},
	{"FetchModSeq", TestFetchModSeq},
	{"UnchangedSince", TestUnchangedSince},
	{"Concurrency", TestConcurrency},
	{"Idle", TestIdle},
}

// TestImmutable is a collection of tests that do not change the state
// of the IMAP server, so they can be run in parallel on the same server.
func TestImmutable(t *testing.T, server *TestServer) {
	immutableTests := []TestFn{
		{"NonAuth", TestNonAuth},
		{"Login", TestLogin},
		{"Search", TestSearch},
		{"ESearch", TestESearch},
		{"Status", TestStatus},
		{"Select", TestSelect},
		{"List", TestList},
		{"Fetch", TestFetch},
		{"Compress", TestCompress},
		{"XApplePushService", TestXApplePushService},
	}
	t.Run("Immutable", func(t *testing.T) {
		for _, test := range immutableTests {
			test := test
			t.Run(test.Name, func(t *testing.T) {
				t.Parallel()
				test.Fn(t, server)
			})
		}
	})
}

type DataStoreExtras interface {
	AddUser(username, password []byte) error
	SendMsg(date time.Time, data io.Reader) error
}

func InitTestServer(filer *iox.Filer, dataStore imapserver.DataStore, extras DataStoreExtras) (*TestServer, error) {
	c := &imapserver.Conn{
		Context: context.Background(),
	}

	const (
		username = "crawshaw@spilled.ink"
		password = "aaaabbbbccccdddd"
	)

	if err := extras.AddUser([]byte(username), []byte(password)); err != nil {
		return nil, fmt.Errorf("AddUser: %v", err)
	}

	_, session, err := dataStore.Login(c, []byte(username), []byte(password))
	if err != nil {
		return nil, fmt.Errorf("imaptest.InitTestServer: login: %v", err)
	}
	if err := initUser(filer, session); err != nil {
		return nil, fmt.Errorf("imaptest.InitTestServer: init user: %v", err)
	}
	session.Close()

	s := &TestServer{
		dataStore: dataStore,
		extras:    extras,
		s: &imapserver.Server{
			TLSConfig: tlstest.ServerConfig,
			DataStore: dataStore,
			Filer:     filer,
			/*Debug: func(sessionID string) io.WriteCloser {
				// TODO: ditch connLog and use this log instead
				return os.Stdout
			},*/
		},
	}
	s.s.Logf = func(format string, v ...interface{}) {
		if s.t == nil {
			panic(fmt.Sprintf("imaptest.TestServer: imapserver called logf before TestServer.Init: "+format, v...))
		}
		s.t.Logf(format, v...) // t changes
	}

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("imaptest.InitTestServer: %v", err)
	}
	s.addr = ln.Addr()
	go func() {
		if err := s.s.ServeTLS(ln); err != nil {
			if err != imapserver.ErrServerClosed {
				if s.t == nil {
					panic(fmt.Sprintf("bad imap test server exit: %v", err))
				}
				s.t.Errorf("bad server exit: %v", err)
			}
		}
	}()

	return s, nil
}

func initUser(filer *iox.Filer, s imap.Session) error {
	if err := s.CreateMailbox([]byte("TestFlagged"), imap.AttrFlagged); err != nil {
		return err
	}

	inbox, err := s.Mailbox([]byte("INBOX"))
	if err != nil {
		return err
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for len(dir) > 1 && filepath.Base(dir) != "spilled.ink" {
		dir = filepath.Dir(dir)
	}
	dir = filepath.Join(dir, "testdata")

	msgFiles := []string{
		"msg1.eml",
		"msg1.eml", // TODO: msg2.eml
		"msg3.eml",
		"msg4.eml",
		"msg5.eml",
	}
	var msgs []*iox.BufferFile
	for _, file := range msgFiles {
		file = filepath.Join(dir, file)
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		data := filer.BufferFile(0)
		_, err = io.Copy(data, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("test session msg %s copy: %v", file, err)
		}
		data.Seek(0, 0)
		msgs = append(msgs, data)
	}

	flags := [][]byte{[]byte("\\Flagged")}
	if _, err := inbox.Append(flags, time.Now(), msgs[0]); err != nil {
		return fmt.Errorf("append msg1.eml: %v", err)
	}
	if _, err := inbox.Append(nil, time.Now(), msgs[1]); err != nil {
		return fmt.Errorf("append repeat msg1.eml: %v", err)
	}
	seq2 := []imapparser.SeqRange{{Min: 2, Max: 2}}
	_, err = inbox.Store(true, seq2, &imapparser.Store{
		Mode:  imapparser.StoreAdd,
		Flags: [][]byte{[]byte(`\Deleted`)},
	})
	if err != nil {
		return fmt.Errorf("marking repeat msg1.eml as \\Deleted: %v", err)
	}
	if err := inbox.Expunge(seq2, nil); err != nil {
		return fmt.Errorf("remove repeat msg1.eml: %v", err)
	}
	for i, data := range msgs[2:] {
		flags := [][]byte{[]byte("\\Junk")}
		if _, err := inbox.Append(flags, time.Now(), data); err != nil {
			return fmt.Errorf("test session loop %d: %v", i, err)
		}
	}
	for _, data := range msgs {
		data.Close()
	}
	return nil
}

func crlf(input string) string { return strings.Replace(input, "\n", "\r", -1) }

type TestServer struct {
	t         testing.TB
	dataStore imapserver.DataStore
	extras    DataStoreExtras
	s         *imapserver.Server
	addr      net.Addr
	sessions  []*TestSession
}

func (server *TestServer) Init(t *testing.T) {
	server.t = t
}

func (server *TestServer) Shutdown() error {
	for _, session := range server.sessions {
		session.Shutdown()
	}
	time.Sleep(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	return server.s.Shutdown(ctx)
}

func (server *TestServer) OpenInbox(t *testing.T) *TestSession {
	s := server.OpenSession(t)
	s.read() // initial * OK
	s.login()
	s.selectCmd("INBOX")
	return s
}

func (server *TestServer) OpenSession(t *testing.T) *TestSession {
	server.t = t // TODO gross, racy. remove
	s := &TestSession{
		t:      t,
		server: server,
	}
	var err error
	s.conn, err = tls.Dial("tcp", s.server.addr.String(), tlstest.ClientConfig)
	if err != nil {
		t.Fatalf("imaptest.OpenSession: %v", err)
	}
	s.br = bufio.NewReader(io.TeeReader(s.conn, &s.connLog))
	s.bw = bufio.NewWriter(io.MultiWriter(s.conn, &s.connLog))
	server.sessions = append(server.sessions, s)
	return s
}

func (server *TestServer) Idle(t *testing.T, mailbox string) *TestSession {
	s := server.OpenInbox(t)
	s.selectCmd(mailbox)
	s.SetName("IDLE " + mailbox)
	s.write("1 IDLE\r\n")
	s.readExpectPrefix("+ idling")
	return s
}

type TestSession struct {
	t      *testing.T
	server *TestServer
	conn   net.Conn
	br     *bufio.Reader
	bw     *bufio.Writer
	flush  func() error
	prefix string

	connLog bytes.Buffer // TODO: use s.Debug
}

func (s *TestSession) Compress() {
	s.write("1 COMPRESS DEFLATE\r\n")

	want := "1 OK DEFLATE active\r\n"
	buf := make([]byte, len(want))
	if n, err := io.ReadFull(s.conn, buf); err != nil {
		s.t.Fatalf("Compress: bad response: %q", string(buf[:n]))
	}
	if string(buf) != want {
		s.t.Fatalf("Compress: unexpected response: %q", string(buf))
	}
	s.connLog.WriteString(want)

	r := flate.NewReader(s.conn)
	w, _ := flate.NewWriter(s.conn, 1)
	s.flush = w.Flush

	s.br = bufio.NewReader(io.TeeReader(r, &s.connLog))
	s.bw = bufio.NewWriter(io.MultiWriter(w, &s.connLog))
}

func (s TestSession) Flush() error {
	if err := s.bw.Flush(); err != nil {
		return err
	}
	if s.flush != nil {
		if err := s.flush(); err != nil {
			return err
		}
	}
	return nil
}

func (s *TestSession) SetName(name string) {
	s.prefix = name + ": "
}

func (s *TestSession) Shutdown() {
	if s.conn == nil {
		return
	}
	if s.t.Failed() {
		s.conn.SetDeadline(time.Now())
		ioutil.ReadAll(s.br)
		s.Flush()
		s.t.Logf("%sconnection log: %s", s.prefix, s.connLog.String())
		s.conn.Close()
	}
	s.conn.Close()
	s.conn = nil
}

func (s *TestSession) read() string {
	if s.t.Failed() {
		s.conn.SetReadDeadline(time.Now())
	} else {
		s.conn.SetDeadline(time.Now().Add(3 * time.Second))
	}
	line, err := s.br.ReadSlice('\n')
	if err != nil {
		s.t.Fatalf("%sread line failed: %v", s.prefix, err)
	}
	if len(line) < 2 {
		s.t.Fatalf("%sempty line with bad CRLF", s.prefix)
		return ""
	}
	if line[len(line)-2] != '\r' {
		s.t.Fatalf("%smissing CRLF on line: %q", s.prefix, line)
	}
	line = line[:len(line)-1]
	return string(line)
}

func (s *TestSession) readExpect(expr string) {
	re, err := regexp.Compile(expr)
	if err != nil {
		s.t.Fatal(err)
	}
	got := s.read()
	if !re.MatchString(got) {
		s.t.Errorf("%sresponse %q does not match %s", s.prefix, got, expr)
	}
}

func (s *TestSession) readExpectPrefix(prefix string) {
	got := s.read()
	if !strings.HasPrefix(got, prefix) {
		s.t.Errorf("%sresponse %q does not have prefix %q", s.prefix, got, prefix)
	}
}

func (s *TestSession) write(format string, v ...interface{}) {
	s.conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(s.bw, format, v...); err != nil {
		s.t.Errorf("%swrite %q failed: %v", s.prefix, format, err)
	}
	if err := s.Flush(); err != nil {
		s.t.Errorf("%sflush %q failed: %v", s.prefix, format, err)
	}
}

func (s *TestSession) login() {
	s.write("t02 LOGIN crawshaw@spilled.ink aaaabbbbccccdddd\r\n")
	if got, want := s.read(), "t02 OK"; !strings.HasPrefix(got, want) {
		s.t.Fatalf("LOGIN response: %q, want prefix %q", got, want)
	}
}

func (s *TestSession) selectCmd(name string) {
	s.write("01 SELECT %s\r\n", name)
	for i := 0; i < 7; i++ {
		if res := s.read(); strings.HasPrefix(res, "01 ") {
			s.t.Errorf("SELECT unexpectedly early completion: %q", res)
			return
		} else if res == "" {
			s.t.Error("SELECT response includes blank line")
			return
		}
	}
	// There are a variable number of return values to SELECT.
	// In particular, UNSEEN may be absent.
	allres := ""
	for i := 0; i < 2; i++ {
		res := s.read()
		if strings.HasPrefix(res, `01 OK`) {
			return
		}
		allres += res
	}
	s.t.Errorf(`response %q is not an "01 OK"`, allres)
}
