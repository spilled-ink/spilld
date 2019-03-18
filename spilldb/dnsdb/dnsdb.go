package dnsdb

import (
	"context"
	"fmt"
	"log"
	"net"
	"regexp"

	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/third_party/dns"
)

type DNS struct {
	DB     *sqlitex.Pool
	Server *dns.Server // created by DNS.Serve method. TODO unexport?
	Logf   func(format string, v ...interface{})
}

func (s *DNS) Serve(ln net.Listener, pc net.PacketConn) error {
	if s.Server == nil {
		s.Server = &dns.Server{}
	}
	s.Server.Listener = ln
	s.Server.PacketConn = pc
	s.Server.Handler = &handler{s: s}
	return s.Server.ActivateAndServe()
}

func (s *DNS) Shutdown(ctx context.Context) error {
	return s.Server.ShutdownContext(ctx)
}

func (s *DNS) lookup(ctx context.Context, queries []query) (result []string, err error) {
	conn := s.DB.Get(ctx)
	if conn == nil {
		return nil, context.Canceled
	}
	defer s.DB.Put(conn)

	stmt := conn.Prep(`SELECT Algorithm, PublicKey FROM DKIMRecords
		WHERE DomainName = $domain AND Selector = $selector;`)

	for _, q := range queries {
		stmt.Reset()
		stmt.SetText("$domain", q.domain)
		stmt.SetText("$selector", q.selector)
		if has, err := stmt.Step(); err != nil {
			return nil, fmt.Errorf("dnsdb: %s._domainkey.%s: %v", q.selector, q.domain, err)
		} else if !has {
			continue
		}
		alg := stmt.GetText("Algorithm")
		pubKey := stmt.GetText("PublicKey")
		stmt.Reset()

		// TODO: handle TXT field size limit? or do it in dns?
		r := fmt.Sprintf("v=DKIM1; k=%s; p=%s", alg, pubKey)
		result = append(result, r)
	}

	return result, nil
}

type handler struct {
	s *DNS
}

var domainRE = regexp.MustCompile(`^(.*)._domainkey.(.*).$`)

type query struct {
	selector string
	domain   string
}

// Implements dns.Handler
func (s *handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	var replyToQuestions []dns.Question
	var queries []query
	// Users set an NS subdomain record for _domainkey.<theirdomain>.
	for _, q := range r.Question {
		if q.Qtype != dns.TypeTXT {
			log.Printf("DEBUG skipping DNS question %v", q)
			continue
		}
		log.Printf("DEBUG DNS TXT request Name=%s", q.Name)
		// We answer "<selector>._domainkey.<theirdomain>" queries here.
		//"<selector>._domainkey.<theirdomain>"
		match := domainRE.FindStringSubmatch(q.Name)
		if len(match) != 3 {
			continue
		}
		queries = append(queries, query{
			selector: match[1],
			domain:   match[2],
		})
	}
	ctx := context.Background() // TODO
	result, err := s.s.lookup(ctx, queries)
	if err != nil {
		s.s.Logf("ServeDNS lookup error: %v", err) // TODO JSON log
		return
	}

	m := new(dns.Msg)
	for i, res := range result {
		if res == "" {
			continue
		}
		q := &r.Question[i]
		replyToQuestions = append(replyToQuestions, *q)
		txt := &dns.TXT{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeTXT,
				Class:  dns.ClassINET,
			},
			Txt: []string{res},
		}
		m.Extra = append(m.Extra, txt)
	}

	r.Question = replyToQuestions
	m.SetReply(r)
	if err := w.WriteMsg(m); err != nil {
		s.s.Logf("WriteMsg error %v", err) // TODO JSON log
	}
}
