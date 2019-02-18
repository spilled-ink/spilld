//+build ignore

package main

import (
	"log"
	"net"

	"crawshaw.io/iox"
	"spilled.ink/imap/imapserver"
	"spilled.ink/util/tlstest"
)

func main() {
	s := &imapserver.Server{
		TLSConfig: tlstest.ServerConfig,
		Filer:     iox.NewFiler(0),
		Logf:      log.Printf,
	}

	ln, err := net.Listen("tcp", "localhost:8993")
	if err != nil {
		panic(err)
	}
	log.Printf("serving IMAP on %s", ln.Addr())
	if err := s.ServeTLS(ln); err != nil {
		panic(err)
	}
}
