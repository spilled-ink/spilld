package spilldb

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"crawshaw.io/iox"
	"crawshaw.io/iox/webfetch"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"golang.org/x/crypto/acme/autocert"
	"spilled.ink/email/msgbuilder"
	"spilled.ink/imap/imapserver"
	"spilled.ink/smtp/smtpserver"
	"spilled.ink/spilldb/boxmgmt"
	"spilled.ink/spilldb/db"
	"spilled.ink/spilldb/deliverer"
	"spilled.ink/spilldb/honeypotdb"
	"spilled.ink/spilldb/imapdb"
	"spilled.ink/spilldb/localsender"
	"spilled.ink/spilldb/processor"
	"spilled.ink/spilldb/smtpdb"
	"spilled.ink/spilldb/webcache"
)

type Server struct {
	Filer *iox.Filer
	DB    *sqlitex.Pool

	CertManager *autocert.Manager
	Version     string
	APNSCert    *tls.Certificate

	Deliverer   *deliverer.Deliverer
	Processor   *processor.Processor
	LocalSender *localsender.LocalSender
	WebFetch    *webfetch.Client
	BoxMgmt     *boxmgmt.BoxMgmt
	MsgBuilder  *msgbuilder.Builder
	Logf        func(format string, v ...interface{})

	cacheDB *sqlitex.Pool

	shutdownFnsMu sync.Mutex
	shutdownFns   []func(context.Context) error
}

func New(filer *iox.Filer, dbDir string) (*Server, error) {
	if filer == nil {
		filer = iox.NewFiler(0)
	}
	s := &Server{
		Filer: filer,
		Logf:  log.Printf,
	}
	logf := func(format string, v ...interface{}) {
		s.Logf(format, v...)
	}

	dbfile := "file::memory:?mode=memory"
	cacheDBFile := "file::memory:?mode=memory"
	if dbDir != "" {
		if err := os.MkdirAll(dbDir, 0770); err != nil {
			return nil, fmt.Errorf("spilldb: initialize dbdir: %v", err)
		}
		dbfile = filepath.Join(dbDir, "spilld.db")
		cacheDBFile = filepath.Join(dbDir, "spilld_cache.db")
	}

	conn, err := sqlite.OpenConn(dbfile, 0)
	if err != nil {
		return nil, fmt.Errorf("spilldb: open main db: %v", err)
	}
	if err := db.Init(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("spilldb: init main db: %v", err)
	}
	if err := conn.Close(); err != nil {
		return nil, fmt.Errorf("spilldb: init main db close: %v", err)
	}

	s.DB, err = sqlitex.Open(dbfile, 0, 24)
	if err != nil {
		return nil, fmt.Errorf("spilldb: open main pool: %v", err)
	}

	s.BoxMgmt, err = boxmgmt.New(filer, s.DB, dbDir)
	if err != nil {
		s.DB.Close()
		return nil, err
	}

	s.cacheDB, err = sqlitex.Open(cacheDBFile, 0, 4)
	if err != nil {
		s.DB.Close()
		s.BoxMgmt.Close()
		return nil, err
	}
	s.WebFetch, err = webcache.New(s.cacheDB, s.Filer, http.DefaultClient, logf)
	if err != nil {
		s.DB.Close()
		s.BoxMgmt.Close()
		s.cacheDB.Close()
		return nil, err
	}

	s.LocalSender = localsender.New(s.DB, s.Filer, s.BoxMgmt)
	s.Processor = processor.NewProcessor(s.DB, s.Filer, s.WebFetch, s.LocalSender.Process)
	s.Deliverer = deliverer.NewDeliverer(s.DB, s.Filer)
	s.MsgBuilder = &msgbuilder.Builder{Filer: filer}

	return s, nil
}

type ServerAddr struct {
	Hostname  string
	Ln        net.Listener
	TLSConfig *tls.Config
}

func (s *Server) Serve(smtp, msa, imap []ServerAddr) error {
	errCh := make(chan error, 8)

	s.shutdownFnsMu.Lock()
	s.shutdownFns = []func(context.Context) error{
		func(ctx context.Context) error { s.LocalSender.Shutdown(ctx); return nil }, // TODO
		func(context.Context) error { s.Deliverer.Shutdown(); return nil },          // TODO
		func(ctx context.Context) error { s.Processor.Shutdown(ctx); return nil },
		func(ctx context.Context) error { s.WebFetch.Shutdown(ctx); return nil },
	}
	s.shutdownFnsMu.Unlock()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Logf("spilldb: message local deliverer starting")
		if err := s.LocalSender.Run(); err != nil {
			errCh <- fmt.Errorf("spilldb.LocalSender: %v", err)
		}
		s.Logf("spilldb: message local deliverer shutdown")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Logf("spilldb: message remote deliverer starting")
		if err := s.Deliverer.Run(); err != nil {
			errCh <- fmt.Errorf("spilldb.Deliverer: %v", err)
		}
		s.Logf("spilldb: message remote deliverer shutdown")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Logf("spilldb: incoming message processor starting")
		if err := s.Processor.Run(); err != nil {
			errCh <- fmt.Errorf("spilldb.Processor: %v", err)
		}
		s.Logf("spilldb: incoming message processor shutdown")
	}()

	for _, addr := range smtp {
		addr := addr
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Logf("spilldb: SMTP %s, %s: starting", addr.Hostname, addr.Ln.Addr())
			if err := s.serveSMTP(addr); err != nil {
				if err != smtpserver.ErrServerClosed {
					errCh <- fmt.Errorf("spilldb SMTP %s: %v", addr.Hostname, err)
				}
			}
			s.Logf("spilldb: SMTP %s, %s: shutdown", addr.Hostname, addr.Ln.Addr())
		}()
	}

	for _, addr := range msa {
		addr := addr
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Logf("spilldb: MSA %s, %s: starting", addr.Hostname, addr.Ln.Addr())
			if err := s.serveMSA(addr); err != nil {
				errCh <- fmt.Errorf("spilldb MSA %s: %v", addr.Hostname, err)
			}
			s.Logf("spilldb: MSA %s, %s: shutdown", addr.Hostname, addr.Ln.Addr())
		}()
	}

	for i, addr := range imap {
		i, addr := i, addr
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.serveIMAP(addr, i == 0); err != nil {
				errCh <- fmt.Errorf("spilldb IMAP %s: %v", addr.Hostname, err)
			}
		}()
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (s *Server) addShutdownFn(fn func(context.Context) error) {
	s.shutdownFnsMu.Lock()
	s.shutdownFns = append(s.shutdownFns, fn)
	s.shutdownFnsMu.Unlock()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.Logf("spilldb: shutdown started")

	shutdownDone := make(chan struct{}, 1)
	go func() {
		select {
		case <-shutdownDone:
		case <-ctx.Done():
			s.Logf("spilldb: shutdown time out, becoming less graceful")
		}
	}()

	// Stage 1: shut down the serving elements.
	var wg sync.WaitGroup

	s.shutdownFnsMu.Lock()
	errCh := make(chan error, len(s.shutdownFns))
	for _, fn := range s.shutdownFns {
		wg.Add(1)
		fn := fn
		go func() {
			defer wg.Done()
			if err := fn(ctx); err != nil {
				errCh <- err
			}
		}()
	}
	s.shutdownFns = nil
	s.shutdownFnsMu.Unlock()

	// Stage 2: bring down the database and filer.
	if err := s.DB.Close(); err != nil {
		s.Logf("spilldb: DB shutdown: %v", err)
	}
	if err := s.cacheDB.Close(); err != nil {
		s.Logf("spilldb: cache DB shutdown: %v", err)
	}
	s.Logf("spilldb: DB shutdown")

	s.Filer = nil

	shutdownDone <- struct{}{}
	s.Logf("spilldb: shutdown complete")
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (s *Server) tlsConfig(addr ServerAddr) (*tls.Config, error) {
	if addr.TLSConfig != nil {
		return addr.TLSConfig, nil
	}
	config := &tls.Config{}

	if s.CertManager != nil {
		hello := &tls.ClientHelloInfo{ServerName: addr.Hostname}
		cert, err := s.CertManager.GetCertificate(hello)
		if err != nil {
			return nil, err
		}
		//config.GetCertificate = s.certManager.GetCertificate TODO ???
		config.Certificates = append(config.Certificates, *cert)
	}
	return config, nil
}

func (s *Server) serveSMTP(addr ServerAddr) error {
	tlsConfig, err := s.tlsConfig(addr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgMaker := smtpdb.New(ctx, s.DB, s.Filer, s.Processor.Process)

	/*gl, err := greylistdb.New(s.dbpool)
	if err != nil {
		log.Fatalf("SMTP failed to start: %v", err)
	}
	gl.Filer = s.filer
	gl.ProcessMsg = msgMaker.NewMessage*/

	honeypot, err := honeypotdb.New(ctx, s.cacheDB, s.Filer, msgMaker.NewMessage)
	if err != nil {
		return err
	}

	const maxMsgSize = 1 << 27
	smtp := &smtpserver.Server{
		Hostname:   addr.Hostname,
		Auth:       honeypot.Auth,
		NewMessage: honeypot.NewMessage,
		MaxSize:    maxMsgSize,
		// TODO Rand:       s.rand,
		AllowNoTLS: true,
		TLSConfig:  tlsConfig,
	}

	s.addShutdownFn(smtp.Shutdown)

	if err := smtp.ServeSTARTTLS(addr.Ln); err != nil {
		if err != smtpserver.ErrServerClosed {
			return err
		}
	}
	return nil
}

func (s *Server) serveMSA(addr ServerAddr) error {
	tlsConfig, err := s.tlsConfig(addr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	doneFn := func(stagingID int64) {
		// We need one of these, or both.
		// It's not clear which without plumbing,
		// but it's fine to give them both a kick.
		s.Deliverer.Deliver(stagingID)
		s.Processor.Process(stagingID)
	}
	msgMaker := smtpdb.New(ctx, s.DB, s.Filer, doneFn)

	const maxMsgSize = 1 << 27
	smtp := &smtpserver.Server{
		Hostname:   addr.Hostname,
		Auth:       msgMaker.Auth,
		NewMessage: msgMaker.NewMessage,
		MaxSize:    maxMsgSize,
		// TODO Rand:       s.rand,
		TLSConfig: tlsConfig,
	}
	s.addShutdownFn(smtp.Shutdown)

	if err := smtp.ServeTLS(addr.Ln); err != nil {
		if err != smtpserver.ErrServerClosed {
			return err
		}
	}
	return nil
}

func (s *Server) serveIMAP(addr ServerAddr, first bool) error {
	tlsConfig, err := s.tlsConfig(addr)
	if err != nil {
		return err
	}

	imap := imapdb.New(tlsConfig, s.DB, s.Filer, s.BoxMgmt, s.Logf)
	imap.Version = s.Version

	if s.APNSCert != nil {
		imap.APNS = &imapserver.APNS{
			Certificate: *s.APNSCert,
		}
		// We only want one APNS notifier running, but we have two IMAP servers.
		imap.NotifyAPNS = first
	}

	s.addShutdownFn(imap.Shutdown)

	apnsLog := ""
	if imap.NotifyAPNS {
		apnsLog = " with APNS"
	}
	s.Logf("spilldb: IMAP %s, %s: starting%s", addr.Hostname, addr.Ln.Addr(), apnsLog)
	defer s.Logf("spilldb: IMAP %s, %s: shutdown", addr.Hostname, addr.Ln.Addr())

	if err := imap.ServeTLS(addr.Ln); err != nil {
		if err != imapserver.ErrServerClosed {
			return err
		}
	}
	return nil
}
