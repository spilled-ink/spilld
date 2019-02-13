package main

import (
	"context"
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"time"

	"crawshaw.io/iox"
	"spilled.ink/spilldb"
)

var version = "unknown" // filled in by "-ldflags=-X main.version=<val>"

func main() {
	log.SetFlags(0)
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("cannot read hostname: %v, using localhost", err)
		hostname = "localhost"
	}

	flagDBDir := flag.String("dbdir", "", "spilldb database directory")
	flagDebugAddr := flag.String("debug_addr", "", "address for debug HTTP")
	flagIMAPHostname := flag.String("imap_hostname", hostname, "hostname for IMAP")
	flagIMAPAddr := flag.String("imap_addr", ":943", "address for IMAP")

	flag.Parse()

	ctx := context.Background()
	filer := iox.NewFiler(0)

	tempdir, err := ioutil.TempDir("", "spilld-")
	if err != nil {
		log.Fatal(err)
	}
	filer.SetTempdir(tempdir)

	log.Printf("spilld (version %s)", version)
	log.Printf("temp dir %s", tempdir)

	if *flagDBDir == "" {
		*flagDBDir = tempdir
	}

	s, err := spilldb.New(filer, *flagDBDir)
	if err != nil {
		log.Fatal(err)
	}
	s.Logf = log.Printf

	var imapAddrs, smtpAddrs, msaAddrs []spilldb.ServerAddr

	if *flagIMAPAddr != "" {
		ln, err := net.Listen("tcp", *flagIMAPAddr)
		if err != nil {
			log.Fatal(err)
		}
		imapAddrs = append(imapAddrs, spilldb.ServerAddr{
			Hostname: *flagIMAPHostname,
			Ln:       ln,
		})
	}

	if *flagDebugAddr != "" {
		debugMux := http.NewServeMux()
		debugMux.HandleFunc("/debug/pprof/", pprof.Index)
		debugMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		debugMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		debugMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		debugMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

		debugServer := &http.Server{Handler: debugMux}
		go func() {
			ln, err := net.Listen("tcp", *flagDebugAddr)
			if err != nil {
				s.Logf("http debug server: %s", err)
				return
			}
			s.Logf("debug HTTP starting on %s", ln.Addr())
			err = debugServer.Serve(ln)
			if err != nil && err != http.ErrServerClosed {
				s.Logf("http debug serving error: %v", err)
			}
		}()
	}

	go func() {
		if err := s.Serve(smtpAddrs, msaAddrs, imapAddrs); err != nil {
			s.Logf("spilldb serve error: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		<-interrupt
		cancel()
	}()
	<-ctx.Done()

	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		s.Shutdown(ctx)
		wg.Done()
	}()
	wg.Wait()

	if err := filer.Shutdown(ctx); err != nil {
		log.Printf("spilld: filer shutdown error: %v", err)
	}
	log.Printf("spilld: shut down")
}
