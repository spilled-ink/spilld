package dns

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

func HelloServer(w ResponseWriter, req *Msg) {
	m := new(Msg)
	m.SetReply(req)

	m.Extra = make([]RR, 1)
	m.Extra[0] = &TXT{Hdr: RR_Header{Name: m.Question[0].Name, Rrtype: TypeTXT, Class: ClassINET, Ttl: 0}, Txt: []string{"Hello world"}}
	w.WriteMsg(m)
}

func HelloServerBadID(w ResponseWriter, req *Msg) {
	m := new(Msg)
	m.SetReply(req)
	m.Id++

	m.Extra = make([]RR, 1)
	m.Extra[0] = &TXT{Hdr: RR_Header{Name: m.Question[0].Name, Rrtype: TypeTXT, Class: ClassINET, Ttl: 0}, Txt: []string{"Hello world"}}
	w.WriteMsg(m)
}

func HelloServerEchoAddrPort(w ResponseWriter, req *Msg) {
	m := new(Msg)
	m.SetReply(req)

	remoteAddr := w.RemoteAddr().String()
	m.Extra = make([]RR, 1)
	m.Extra[0] = &TXT{Hdr: RR_Header{Name: m.Question[0].Name, Rrtype: TypeTXT, Class: ClassINET, Ttl: 0}, Txt: []string{remoteAddr}}
	w.WriteMsg(m)
}

func AnotherHelloServer(w ResponseWriter, req *Msg) {
	m := new(Msg)
	m.SetReply(req)

	m.Extra = make([]RR, 1)
	m.Extra[0] = &TXT{Hdr: RR_Header{Name: m.Question[0].Name, Rrtype: TypeTXT, Class: ClassINET, Ttl: 0}, Txt: []string{"Hello example"}}
	w.WriteMsg(m)
}

func RunLocalUDPServer(laddr string) (*Server, string, error) {
	server, l, _, err := RunLocalUDPServerWithFinChan(laddr)

	return server, l, err
}

func RunLocalUDPServerWithFinChan(laddr string, opts ...func(*Server)) (*Server, string, chan error, error) {
	pc, err := net.ListenPacket("udp", laddr)
	if err != nil {
		return nil, "", nil, err
	}
	server := &Server{PacketConn: pc, ReadTimeout: time.Hour, WriteTimeout: time.Hour}

	waitLock := sync.Mutex{}
	waitLock.Lock()
	server.NotifyStartedFunc = waitLock.Unlock

	// fin must be buffered so the goroutine below won't block
	// forever if fin is never read from. This always happens
	// in RunLocalUDPServer and can happen in TestShutdownUDP.
	fin := make(chan error, 1)

	for _, opt := range opts {
		opt(server)
	}

	go func() {
		fin <- server.ActivateAndServe()
		pc.Close()
	}()

	waitLock.Lock()
	return server, pc.LocalAddr().String(), fin, nil
}

func RunLocalTCPServer(laddr string) (*Server, string, error) {
	server, l, _, err := RunLocalTCPServerWithFinChan(laddr)

	return server, l, err
}

func RunLocalTCPServerWithFinChan(laddr string) (*Server, string, chan error, error) {
	l, err := net.Listen("tcp", laddr)
	if err != nil {
		return nil, "", nil, err
	}

	server := &Server{Listener: l, ReadTimeout: time.Hour, WriteTimeout: time.Hour}

	waitLock := sync.Mutex{}
	waitLock.Lock()
	server.NotifyStartedFunc = waitLock.Unlock

	// See the comment in RunLocalUDPServerWithFinChan as to
	// why fin must be buffered.
	fin := make(chan error, 1)

	go func() {
		fin <- server.ActivateAndServe()
		l.Close()
	}()

	waitLock.Lock()
	return server, l.Addr().String(), fin, nil
}

func RunLocalTLSServer(laddr string, config *tls.Config) (*Server, string, error) {
	l, err := tls.Listen("tcp", laddr, config)
	if err != nil {
		return nil, "", err
	}

	server := &Server{Listener: l, ReadTimeout: time.Hour, WriteTimeout: time.Hour}

	waitLock := sync.Mutex{}
	waitLock.Lock()
	server.NotifyStartedFunc = waitLock.Unlock

	go func() {
		server.ActivateAndServe()
		l.Close()
	}()

	waitLock.Lock()
	return server, l.Addr().String(), nil
}

func HelloServerCompress(w ResponseWriter, req *Msg) {
	m := new(Msg)
	m.SetReply(req)
	m.Extra = make([]RR, 1)
	m.Extra[0] = &TXT{Hdr: RR_Header{Name: m.Question[0].Name, Rrtype: TypeTXT, Class: ClassINET, Ttl: 0}, Txt: []string{"Hello world"}}
	m.Compress = true
	w.WriteMsg(m)
}

type maxRec struct {
	max int
	sync.RWMutex
}

var M = new(maxRec)

func HelloServerLargeResponse(resp ResponseWriter, req *Msg) {
	m := new(Msg)
	m.SetReply(req)
	m.Authoritative = true
	m1 := 0
	M.RLock()
	m1 = M.max
	M.RUnlock()
	for i := 0; i < m1; i++ {
		aRec := &A{
			Hdr: RR_Header{
				Name:   req.Question[0].Name,
				Rrtype: TypeA,
				Class:  ClassINET,
				Ttl:    0,
			},
			A: net.ParseIP(fmt.Sprintf("127.0.0.%d", i+1)).To4(),
		}
		m.Answer = append(m.Answer, aRec)
	}
	resp.WriteMsg(m)
}

func TestShutdownTCP(t *testing.T) {
	s, _, fin, err := RunLocalTCPServerWithFinChan(":0")
	if err != nil {
		t.Fatalf("unable to run test server: %v", err)
	}
	err = s.Shutdown()
	if err != nil {
		t.Fatalf("could not shutdown test TCP server, %v", err)
	}
	select {
	case err := <-fin:
		if err != nil {
			t.Errorf("error returned from ActivateAndServe, %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("could not shutdown test TCP server. Gave up waiting")
	}
}

func init() {
	testShutdownNotify = &sync.Cond{
		L: new(sync.Mutex),
	}
}

func TestShutdownUDP(t *testing.T) {
	s, _, fin, err := RunLocalUDPServerWithFinChan(":0")
	if err != nil {
		t.Fatalf("unable to run test server: %v", err)
	}
	err = s.Shutdown()
	if err != nil {
		t.Errorf("could not shutdown test UDP server, %v", err)
	}
	select {
	case err := <-fin:
		if err != nil {
			t.Errorf("error returned from ActivateAndServe, %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("could not shutdown test UDP server. Gave up waiting")
	}
}

func TestServerStartStopRace(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		s, _, _, err := RunLocalUDPServerWithFinChan(":0")
		if err != nil {
			t.Fatalf("could not start server: %s", err)
		}
		go func() {
			defer wg.Done()
			if err := s.Shutdown(); err != nil {
				t.Errorf("could not stop server: %s", err)
			}
		}()
	}
	wg.Wait()
}

func TestServerReuseport(t *testing.T) {
	if !supportsReusePort {
		t.Skip("reuseport is not supported")
	}

	startServer := func(addr string) (*Server, chan error) {
		wait := make(chan struct{})
		srv := &Server{
			Net:               "udp",
			Addr:              addr,
			NotifyStartedFunc: func() { close(wait) },
			ReusePort:         true,
		}

		fin := make(chan error, 1)
		go func() {
			fin <- srv.ListenAndServe()
		}()

		select {
		case <-wait:
		case err := <-fin:
			t.Fatalf("failed to start server: %v", err)
		}

		return srv, fin
	}

	srv1, fin1 := startServer(":0") // :0 is resolved to a random free port by the kernel
	srv2, fin2 := startServer(srv1.PacketConn.LocalAddr().String())

	if err := srv1.Shutdown(); err != nil {
		t.Fatalf("failed to shutdown first server: %v", err)
	}
	if err := srv2.Shutdown(); err != nil {
		t.Fatalf("failed to shutdown second server: %v", err)
	}

	if err := <-fin1; err != nil {
		t.Fatalf("first ListenAndServe returned error after Shutdown: %v", err)
	}
	if err := <-fin2; err != nil {
		t.Fatalf("second ListenAndServe returned error after Shutdown: %v", err)
	}
}

func TestResponseAfterClose(t *testing.T) {
	testError := func(name string, err error) {
		t.Helper()

		expect := fmt.Sprintf("dns: %s called after Close", name)
		if err == nil {
			t.Errorf("expected error from %s after Close", name)
		} else if err.Error() != expect {
			t.Errorf("expected explicit error from %s after Close, expected %q, got %q", name, expect, err)
		}
	}

	rw := &response{
		closed: true,
	}

	_, err := rw.Write(make([]byte, 2))
	testError("Write", err)

	testError("WriteMsg", rw.WriteMsg(new(Msg)))
}

func TestResponseDoubleClose(t *testing.T) {
	rw := &response{
		closed: true,
	}
	if err, expect := rw.Close(), "dns: connection already closed"; err == nil || err.Error() != expect {
		t.Errorf("Close did not return expected: error %q, got: %v", expect, err)
	}
}
