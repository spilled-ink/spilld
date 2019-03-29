package smtpclient

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

type Client struct {
	LocalHostname string   // name of this host
	LocalAddr     net.Addr // address on this host to send from
	Resolver      *net.Resolver

	limiter chan struct{} // per open connection
}

func NewClient(localHostname string, maxConcurrent int) *Client {
	return &Client{
		Resolver:      net.DefaultResolver,
		LocalHostname: localHostname,
		limiter:       make(chan struct{}, maxConcurrent),
	}
}

type Delivery struct {
	Recipient string
	Code      int
	Details   string
	Date      time.Time
	Error     error
}

func (d Delivery) Success() bool     { return d.Code == 250 && d.Error == nil }
func (d Delivery) PermFailure() bool { return d.Code >= 500 }
func (d Delivery) TempFailure() bool { return (d.Code >= 400 && d.Code < 500) || d.Error != nil }

func (c *Client) Send(ctx context.Context, from string, recipients []string, contents io.ReaderAt, contentSize int64) (results []Delivery, err error) {
	mxDomain := make(map[string]string) // domain name -> MX record (a local lookup cache)
	spools := make(map[string][]string) // MX spool -> recipients

	for _, to := range recipients {
		domain := to[strings.LastIndexByte(to, '@')+1:]
		mxAddr := mxDomain[domain]
		if mxAddr != "" {
			spools[mxAddr] = append(spools[mxAddr], to)
			continue
		}
		mxs, err := c.Resolver.LookupMX(ctx, domain)
		if err != nil {
			continue
		}
		pref := uint16(50000)
		for _, opt := range mxs {
			if opt.Pref < pref {
				mxAddr = opt.Host
				pref = opt.Pref
			}
		}
		if mxAddr == "" {
			continue
		}

		mxDomain[domain] = mxAddr
		spools[mxAddr] = append(spools[mxAddr], to)
	}

	select {
	case <-ctx.Done():
		return nil, context.Canceled
	default:
	}

	deliveries := 0
	for _, rcpts := range spools {
		deliveries += len(rcpts)
	}

	resultsCh := make(chan Delivery, deliveries)
	go func() {
		for mxAddr, rcpts := range spools {
			r := io.NewSectionReader(contents, 0, contentSize)
			results := c.send(ctx, mxAddr+":25", from, rcpts, r)
			for _, res := range results {
				resultsCh <- res
			}
		}
	}()

	results = make([]Delivery, deliveries)
	for i := range results {
		results[i] = <-resultsCh
	}
	return results, nil
}

func (c *Client) send(ctx context.Context, mxAddr string, from string, recipients []string, r io.Reader) (results []Delivery) {
	results = make([]Delivery, len(recipients))
	for i, rcpt := range recipients {
		results[i].Recipient = rcpt
	}
	allErr := func(err error) []Delivery {
		for i := range results {
			if results[i].Code == 0 {
				results[i].Error = err
			}
		}
		return results
	}

	select {
	case c.limiter <- struct{}{}:
	case <-ctx.Done():
		return allErr(context.Canceled)
	}
	defer func() { <-c.limiter }()

	dialer := &net.Dialer{
		Resolver:  c.Resolver,
		LocalAddr: c.LocalAddr,
	}
	tcpConn, err := dialer.DialContext(ctx, "tcp", mxAddr)
	if err != nil {
		return allErr(err)
	}
	host, _, _ := net.SplitHostPort(mxAddr)
	mxConn, err := smtp.NewClient(tcpConn, host)
	if err != nil {
		return allErr(err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		mxConn.Close()
	}()
	defer func() { close(done) }()

	tlsConfig := &tls.Config{
		// TODO: do better for servers we know we can trust:
		// https://starttls-everywhere.org/
		InsecureSkipVerify: true,
	}
	if err := mxConn.Hello(c.LocalHostname); err != nil {
		return allErr(err)
	}
	if err := mxConn.StartTLS(tlsConfig); err != nil {
		return allErr(err)
	}
	if err := mxConn.Mail(from); err != nil {
		return allErr(err)
	}
	deliverAttempt := 0
	for i, to := range recipients {
		if rcptErr := mxConn.Rcpt(to); rcptErr != nil {
			if tperr, _ := rcptErr.(*textproto.Error); tperr != nil {
				results[i].Code = tperr.Code
				results[i].Details = tperr.Msg
				continue
			}
			err = rcptErr
			break
		}
		deliverAttempt++
	}
	if err != nil {
		return allErr(err)
	}
	if deliverAttempt == 0 {
		return results
	}

	w, err := mxConn.Data()
	if err != nil {
		return allErr(err)
	}
	if _, err := io.Copy(w, r); err != nil {
		return allErr(err)
	}
	if err := w.Close(); err != nil {
		return allErr(err)
	}
	if err := mxConn.Quit(); err != nil {
		return allErr(err)
	}
	for i := range results {
		if results[i].Code == 0 && results[i].Error == nil {
			results[i].Code = 250
		}
	}
	return results
}
