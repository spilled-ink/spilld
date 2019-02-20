package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"crawshaw.io/iox"
	"spilled.ink/spilldb"
	"spilled.ink/util/devcert"
)

var version = "unknown" // filled in by "-ldflags=-X main.version=<val>"

func main() {
	log.SetFlags(0)
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("cannot read hostname: %v, using localhost", err)
		hostname = "localhost"
	}

	flagDev := flag.Bool("dev", false, `development server, local CA is used and backup ports are opened with an 8-prefix`)
	flagDBDir := flag.String("dbdir", "", "spilldb database directory")
	flagDebugAddr := flag.String("debug_addr", "", "HTTP address for the debug server (do *not* expose to the public)")
	flagIMAPHostname := flag.String("imap_hostname", hostname, "IMAP hostname")
	flagIMAPAddr := flag.String("imap_addr", ":943", "IMAP address")
	flagSMTPHostname := flag.String("smtp_hostname", hostname, "SMTP hostname")
	flagSMTPAddr := flag.String("smtp_addr", ":25", "SMTP address")
	flagMSAHostname := flag.String("msa_hostname", hostname, "MSA hostname")
	flagMSAAddr := flag.String("msa_addr", ":465", "MSA (mail submission) address")
	flagHTTPAddr := flag.String("http_addr", ":80", "address for HTTP (used by Let's Encrypt autocert)")

	flag.Parse()

	ctx := context.Background()
	filer := iox.NewFiler(0)

	tempdir, err := ioutil.TempDir("", "spilld-")
	if err != nil {
		log.Fatal(err)
	}
	filer.SetTempdir(tempdir)

	log.Printf("spilld, version %s, starting at %s", version, time.Now())

	if *flagDBDir == "" {
		*flagDBDir = tempdir
	}

	var certManager *autocert.Manager
	var tlsConfig *tls.Config
	if *flagDev {
		log.Printf("***DEVELOPMENT MODE***")
		tlsConfig, err = devcert.Config()
		if err != nil {
			log.Fatal(err)
		}
	} else {
		var hosts []string
		if *flagIMAPHostname != "" {
			hosts = append(hosts, *flagIMAPHostname)
		}
		if *flagSMTPHostname != "" {
			hosts = append(hosts, *flagSMTPHostname)
		}
		if *flagMSAHostname != "" {
			hosts = append(hosts, *flagMSAHostname)
		}
		certManager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(hosts...),
			Cache:      autocert.DirCache(filepath.Join(*flagDBDir, "tls_certs")),
		}
		// TODO: this clobbers spilldb.Server.tlsConfig,
		// which has a necessary hack for SMTP.
		tlsConfig = &tls.Config{
			GetCertificate: certManager.GetCertificate,
		}
	}

	log.Printf("temp dir %s", tempdir)

	s, err := spilldb.New(filer, *flagDBDir)
	if err != nil {
		log.Fatal(err)
	}
	s.CertManager = certManager
	s.Logf = log.Printf

	var imapAddrs, smtpAddrs, msaAddrs []spilldb.ServerAddr

	if *flagIMAPAddr != "" {
		ln, err := net.Listen("tcp", *flagIMAPAddr)
		if err != nil {
			log.Fatal(err)
		}
		addr := spilldb.ServerAddr{
			Hostname:  *flagIMAPHostname,
			Ln:        ln,
			TLSConfig: tlsConfig,
		}
		imapAddrs = append(imapAddrs, addr)
	}
	if *flagSMTPAddr != "" {
		ln, err := net.Listen("tcp", *flagSMTPAddr)
		if err != nil {
			log.Fatal(err)
		}
		smtpAddrs = append(smtpAddrs, spilldb.ServerAddr{
			Hostname:  *flagSMTPHostname,
			Ln:        ln,
			TLSConfig: tlsConfig,
		})
	}
	if *flagMSAAddr != "" {
		ln, err := net.Listen("tcp", *flagMSAAddr)
		if err != nil {
			log.Fatal(err)
		}
		msaAddrs = append(msaAddrs, spilldb.ServerAddr{
			Hostname:  *flagMSAHostname,
			Ln:        ln,
			TLSConfig: tlsConfig,
		})
	}

	// TODO: call debugServer.Shutdown
	if *flagDev && *flagDebugAddr == "" {
		*flagDebugAddr = ":1380"
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

	if *flagDev {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "hi\n")
		})
		srv := &http.Server{
			TLSConfig: tlsConfig,
			Handler:   handler,
			Addr:      ":8443",
		}
		go func() {
			if err := srv.ListenAndServeTLS("", ""); err != nil {
				log.Fatal(err)
			}
		}()
	}

	// TODO: call certmanager debugServer.Shutdown
	if certManager != nil && *flagHTTPAddr != "" {
		go func() {
			err := http.ListenAndServe(*flagHTTPAddr, certManager.HTTPHandler(nil))
			if err != nil && err != http.ErrServerClosed {
				log.Fatalf("HTTP: %v", err)
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
