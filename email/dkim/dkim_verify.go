package dkim

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

var (
	ErrNotSigned               = errors.New("dkim: no signature")
	ErrMalformed               = errors.New("dkim: signature is malformed")
	ErrBadVersion              = errors.New("dkim: bad signature version")
	ErrBadSignatureData        = errors.New("dkim: bad signature data")
	ErrBadSignature            = errors.New("dkim: bad signature")
	ErrBadBodyLimit            = errors.New("dkim: bad body limit")
	ErrBadDomainKey            = errors.New("dkim: domain record provided bad key")
	ErrNoVersion               = errors.New("dkim: missing version")
	ErrNoAlgorithm             = errors.New("dkim: no algorithm")
	ErrNoSignatureData         = errors.New("dkim: no signature data")
	ErrNoBodyHash              = errors.New("dkim: no body hash")
	ErrNoDomain                = errors.New("dkim: no domain")
	ErrNoSelector              = errors.New("dkim: no selector")
	ErrUnknownAlgorithm        = errors.New("dkim: unknown algorithm (only rsa-sha1 and rsa-sh256 supported)")
	ErrUnknownQueryMethod      = errors.New("dkim: unknown key query method (only dns/text supported)")
	ErrUnknownCanonicalization = errors.New("dkim: unknown canonicalization (only simple and relaxed supported)")
	ErrNoTXTRecord             = errors.New("dkim: no domain TXT record found")
	ErrUnknownDomainKeyType    = errors.New("dkim: bad domain key type (only rsa supported)")
	ErrNoDomainKeyData         = errors.New("dkim: domain record contains no key data")
	ErrShortBody               = errors.New("dkim: body is shorter than specified body limit")
	ErrBadBodyHash             = errors.New("dkim: body hash does not match")
	ErrRSAVerifyFailed         = errors.New("dkim: RSA verification error")
)

// A Verifier verifies DKIM-Signature headers in email.
type Verifier struct {
	LookupTXT func(ctx context.Context, domain string) (txts []string, ttl int, err error)
}

// Verify verifies the DKIM-Signature header in an email.
// TODO: verify all DKIM-Signatures, not just the first.
func (v *Verifier) Verify(ctx context.Context, email io.ReadSeeker) error {
	hdr, err := findDKIMSignature(email)
	if err != nil {
		return err
	}

	var hasVersion bool
	var algo crypto.Hash
	var selector, domain string
	var bodyLimit int64
	var headers [][]byte
	var sig, bodyHash []byte
	canonHeader, canonBody := "simple", "simple"

	off := 0
	for len(hdr) > 0 {
		i := bytes.IndexByte(hdr, ';')
		var part []byte
		if i >= 0 {
			part = bytes.TrimSpace(hdr[:i])
			hdr = hdr[i+1:]
			off += i + 1
		} else {
			part = bytes.TrimSpace(hdr)
			hdr = nil
		}

		i = bytes.IndexByte(part, '=')
		if i == -1 {
			if len(bytes.TrimSpace(part)) == 0 {
				continue
			}
			return ErrMalformed
		}
		k, v := bytes.TrimSpace(part[:i]), bytes.TrimSpace(part[i+1:])

		switch string(k) {
		case "v":
			if string(v) == "1" {
				hasVersion = true
			} else {
				return ErrBadVersion
			}
		case "a":
			switch string(v) {
			case "rsa-sha1":
				algo = crypto.SHA1
			case "rsa-sha256":
				algo = crypto.SHA256
			default:
				return ErrUnknownAlgorithm
			}
		case "c":
			v := v
			if i := bytes.IndexByte(v, '/'); i > 0 {
				switch string(v[i+1:]) {
				case "simple":
					canonBody = "simple"
				case "relaxed":
					canonBody = "relaxed"
				default:
					return ErrUnknownCanonicalization
				}
				v = v[:i]
			}
			switch string(v) {
			case "simple":
				canonHeader = "simple"
			case "relaxed":
				canonHeader = "relaxed"
			default:
				return ErrUnknownCanonicalization
			}
		case "d":
			domain = string(v) // TODO: check this is a grammatical domain
		case "s":
			selector = string(v) // TODO: check this is a grammatical selector
		case "h":
			v := v
			for len(v) > 0 {
				h := v
				if i := bytes.IndexByte(v, ':'); i > 0 {
					h, v = v[:i], v[i+1:]
				} else {
					v = nil
				}
				h = bytes.TrimSpace(h)
				headers = append(headers, h)
			}
		case "b":
			orig := v
			v = v[:0]
			for _, c := range orig {
				switch c {
				case ' ', '\t', '\r', '\n':
				default:
					v = append(v, c)
				}
			}
			sig = make([]byte, base64.StdEncoding.DecodedLen(len(v)))
			n, err := base64.StdEncoding.Decode(sig, v)
			if err != nil {
				return ErrBadSignatureData
			}
			v = v[:0]
			sig = sig[:n]
		case "bh":
			orig := v
			v = v[:0]
			for _, c := range orig {
				switch c {
				case ' ', '\t', '\r', '\n':
				default:
					v = append(v, c)
				}
			}
			bodyHash = make([]byte, base64.StdEncoding.DecodedLen(len(v)))
			n, err := base64.StdEncoding.Decode(bodyHash, v)
			if err != nil {
				return ErrBadSignatureData
			}
			bodyHash = bodyHash[:n]
		case "q": // optional
			if string(v) != "dns/txt" {
				return ErrUnknownQueryMethod
			}
		case "l": // optional
			var err error
			bodyLimit, err = strconv.ParseInt(string(v), 10, 64)
			if err != nil {
				return ErrBadBodyLimit
			}
		}
	}

	if algo == 0 {
		return ErrNoAlgorithm
	}
	if domain == "" {
		return ErrNoDomain
	}
	if selector == "" {
		return ErrNoSelector
	}
	if !hasVersion {
		return ErrNoVersion
	}
	if len(sig) == 0 {
		return ErrNoSignatureData
	}
	if len(bodyHash) == 0 {
		return ErrNoBodyHash
	}

	verifiedBodyHash, err := hashBody(canonBody, bodyLimit, algo, email)
	if err != nil {
		return err
	}
	if testSkipBody {
		// Convenient for unit tests derived from real-world email.
		// Most of the complexity in DKIM verification is getting
		// the header hashing right.
		verifiedBodyHash = bodyHash
	}
	if !bytes.Equal(bodyHash, verifiedBodyHash) {
		return ErrBadBodyHash
	}

	h := algo.New()

	switch canonHeader {
	case "relaxed":
		if err := relaxedHeaders(h, email, headers); err != nil {
			return err
		}
		if false {
			buf := new(bytes.Buffer)
			if err := relaxedHeaders(buf, email, headers); err != nil {
				return err
			}
			fmt.Printf("relaxed headers: %q\n", buf.String())
		}
	case "simple":
		if err := simpleHeaders(h, email, headers); err != nil {
			return err
		}
		if false {
			buf := new(bytes.Buffer)
			if err := simpleHeaders(buf, email, headers); err != nil {
				return err
			}
			fmt.Printf("simple headers: %q\n", buf.String())
		}
	}

	pubKey, err := v.lookupPublicKey(ctx, selector+"._domainkey."+domain)
	if err != nil {
		return err
	}

	if err := rsa.VerifyPKCS1v15(pubKey, algo, h.Sum(nil), sig); err != nil {
		return ErrRSAVerifyFailed
	}
	return nil
}

func hashBody(canonBody string, bodyLimit int64, algo crypto.Hash, email io.ReadSeeker) ([]byte, error) {
	// Skip over the headers to get to the body
	if _, err := email.Seek(0, 0); err != nil {
		return nil, err
	}
	r := bufio.NewReader(email)
	for {
		if _, err := r.ReadBytes('\n'); err != nil {
			return nil, err
		}
		b, err := r.Peek(2)
		if err != nil {
			return nil, err
		}
		if b[0] == '\r' && b[1] == '\n' {
			if _, err := r.Discard(2); err != nil {
				return nil, err
			}
			break // reached the end of the headers
		}
	}
	body := io.Reader(r)

	if bodyLimit != 0 {
		body = io.LimitReader(body, bodyLimit)
	}
	bh := algo.New()
	switch canonBody {
	case "relaxed":
		body = newRelaxedBody(body)
	case "simple":
		body = newSimpleBody(body)
	}
	if n, err := io.Copy(bh, body); err != nil {
		return nil, fmt.Errorf("dkim: body read failed: %v", err)
	} else if bodyLimit != 0 && n != bodyLimit {
		return nil, ErrShortBody
	}
	return bh.Sum(nil), nil
}

func findDKIMSignature(r io.Reader) ([]byte, error) {
	hdrBuf := new(bytes.Buffer)
	if err := readHeader(hdrBuf, r, dkimSigHeader); err != nil {
		return nil, err
	}
	hdr := hdrBuf.Bytes()
	if len(hdr) == 0 {
		return nil, ErrNotSigned
	}
	if len(hdr) < len(dkimSigHeader) {
		return nil, ErrBadSignature
	}
	hdr = hdr[len(dkimSigHeader):]
	return hdr, nil
}

var crlf = []byte{'\r', '\n'}

func readHeader(dst io.Writer, src io.Reader, name []byte) (err error) {
	write := func(p []byte) {
		if err != nil {
			return
		}
		_, err = dst.Write(p)
	}

	s := bufio.NewScanner(src)
	defer func() {
		if err == nil {
			err = s.Err()
		}
	}()
	for s.Scan() {
		b := s.Bytes()
		if len(b) == 0 {
			break // headers are done
		}
		if len(b) >= len(name) && bytes.EqualFold(b[:len(name)], name) {
			write(b)
			write(crlf)
			for s.Scan() {
				b := s.Bytes()
				if len(b) == 0 || (b[0] != ' ' && b[0] != '\t') {
					return err
				}
				write(b)
				write(crlf)
			}
			return err
		}
	}
	return err
}

type byteWriter struct {
	dst io.Writer
	buf [1]byte
	err error
}

func (b *byteWriter) writeByte(c byte) {
	if b.err != nil {
		return
	}
	b.buf[0] = c
	_, b.err = b.dst.Write(b.buf[:])
}

// writeFWS writes to dst while converting all Folding White Space (FWS) to a single ' '.
func (b *byteWriter) writeFWS(p []byte, lastWasWS bool) {
	if b.err != nil {
		return
	}
	out := p[:0]
	for _, c := range p {
		switch c {
		case ' ', '\t', '\r', '\n':
			if !lastWasWS {
				out = append(out, ' ')
				lastWasWS = true
			}
		default:
			out = append(out, c)
			lastWasWS = false
		}
	}
	_, b.err = b.dst.Write(out)
}

func readRelaxedHeader(dst io.Writer, src io.Reader, name []byte) error {
	w := &byteWriter{dst: dst}
	s := bufio.NewScanner(src)

	for s.Scan() {
		b := s.Bytes()
		if len(b) == 0 {
			break // headers are done
		}
		if len(b) >= len(name) && bytes.EqualFold(b[:len(name)], name) {
			w.writeFWS(name, false)
			b = b[len(name):]
			if i := bytes.IndexByte(b, ':'); i >= 0 {
				b = b[i+1:]
			}
			w.writeByte(':')
			w.writeFWS(bytes.TrimSpace(b), false)
			for s.Scan() {
				b := s.Bytes()
				if len(b) == 0 || (b[0] != ' ' && b[0] != '\t') {
					break
				}

				w.writeByte(' ')
				w.writeFWS(bytes.TrimSpace(b), true)
			}
			w.writeByte('\r')
			w.writeByte('\n')
			break
		}
	}

	if w.err != nil {
		return w.err
	}
	if err := s.Err(); err != nil {
		return err
	}
	return nil
}

var dkimSigHeader = []byte("DKIM-Signature:")
var semicolon = []byte{';'}

func simpleHeaders(dst io.Writer, src io.ReadSeeker, headerNames [][]byte) error {
	for _, name := range headerNames {
		if _, err := src.Seek(0, 0); err != nil {
			return err
		}
		if err := readHeader(dst, src, name); err != nil {
			return err
		}
	}
	if _, err := src.Seek(0, 0); err != nil {
		return err
	}

	buf := new(bytes.Buffer)
	if err := readHeader(buf, src, dkimSigHeader); err != nil {
		return err
	}
	b := bytes.TrimRight(buf.Bytes(), "\r\n")
	parts := bytes.Split(b, semicolon)
	for partNum, part := range parts {
		if partNum > 0 {
			if _, err := io.WriteString(dst, ";"); err != nil {
				return err
			}
		}
		i := bytes.IndexByte(part, '=')
		if i == -1 {
			if _, err := dst.Write(part); err != nil {
				return err
			}
			continue
		}
		k, v := part[:i], part[i+1:]
		if ktrim := bytes.TrimSpace(k); len(ktrim) == 1 && ktrim[0] == 'b' {
			v = v[:0]
		}
		part = append(part[:0], k...)
		part = append(part, '=')
		part = append(part, v...)
		if _, err := dst.Write(part); err != nil {
			return err
		}
	}
	return nil
}

var dkimSigHeaderLower = []byte("dkim-signature")

func relaxedHeaders(dst io.Writer, src io.ReadSeeker, headerNames [][]byte) error {
	// First write all headers from h=.
	for _, name := range headerNames {
		toLower(name)
	}
headers:
	for i, name := range headerNames {
		for j := 0; j < i; j++ {
			// When a header is repeated in the h= list,
			// it is only added to the hash once.
			// (Also i is always small, no need for hashing here.)
			if bytes.Equal(name, headerNames[j]) {
				continue headers // repeat, skip
			}
		}
		if _, err := src.Seek(0, 0); err != nil {
			return err
		}
		if err := readRelaxedHeader(dst, src, name); err != nil {
			return err
		}
	}

	// Collect the DKIM-Signature header and write it with
	// the b= field blanked out and final CRLF removed.
	if _, err := src.Seek(0, 0); err != nil {
		return err
	}
	buf := new(bytes.Buffer)
	if err := readRelaxedHeader(buf, src, dkimSigHeaderLower); err != nil {
		return err
	}
	parts := bytes.Split(bytes.TrimRight(buf.Bytes(), "\r\n"), semicolon)
	for i, part := range parts {
		if i > 0 {
			if _, err := io.WriteString(dst, ";"); err != nil {
				return err
			}
		}
		if i := bytes.IndexByte(part, '='); i > 0 {
			if k := bytes.TrimSpace(part[:i]); len(k) == 1 && k[0] == 'b' {
				if _, err := io.WriteString(dst, " b="); err != nil {
					return err
				}
				continue
			}
		}
		if _, err := dst.Write(part); err != nil {
			return err
		}
	}

	return nil
}

func toLower(b []byte) {
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c - 'A' + 'a'
		}
	}
}

func defaultLookupTXT(ctx context.Context, domain string) (txts []string, ttl int, err error) {
	txts, err = net.DefaultResolver.LookupTXT(ctx, domain)
	if err != nil {
		return nil, 0, err
	}
	return txts, 60, nil
}

var testPublicKeyHook func(domain string) *rsa.PublicKey
var testSkipBody bool

func (v *Verifier) lookupPublicKey(ctx context.Context, domain string) (*rsa.PublicKey, error) {
	if testPublicKeyHook != nil {
		return testPublicKeyHook(domain), nil
	}

	lookupFn := v.LookupTXT
	if lookupFn == nil {
		lookupFn = defaultLookupTXT
	}
	txts, ttl, err := lookupFn(ctx, domain)
	if err != nil {
		return nil, err
	}
	if len(txts) == 0 {
		return nil, ErrNoTXTRecord
	}

	var txt []byte
	for _, v := range txts {
		txt = append(txt, v...)
	}

	var pubKeyData []byte

	for len(txt) > 0 {
		i := bytes.IndexByte(txt, ';')
		var part []byte
		if i >= 0 {
			part = bytes.TrimSpace(txt[:i])
			txt = txt[i+1:]
		} else {
			part = bytes.TrimSpace(txt)
			txt = nil
		}
		if len(part) == 0 {
			continue
		}

		i = bytes.IndexByte(part, '=')
		if i == -1 {
			return nil, ErrMalformed
		}
		k, v := part[:i], part[i+1:]

		if len(k) != 1 {
			return nil, ErrBadDomainKey
		}
		switch k[0] {
		case 'k':
			if string(v) != "rsa" {
				return nil, ErrUnknownDomainKeyType
			}
		case 'p':
			pubKeyData = make([]byte, base64.StdEncoding.DecodedLen(len(v)))
			n, err := base64.StdEncoding.Decode(pubKeyData, v)
			if err != nil {
				return nil, ErrBadDomainKey
			}
			pubKeyData = pubKeyData[:n]
		}
	}

	if len(pubKeyData) == 0 {
		return nil, ErrNoDomainKeyData
	}

	pk, err := x509.ParsePKIXPublicKey(pubKeyData)
	if err != nil {
		return nil, fmt.Errorf("dkim: parsing public key: %v", err)
	}
	pubKey, ok := pk.(*rsa.PublicKey)
	if !ok {
		return nil, ErrBadDomainKey
	}

	// TODO: cache keys for the TTL of the resolver
	_ = ttl

	return pubKey, nil
}

// newSimpleBody implements the "simple" Body Canonicalization
// Algorithm from RFC 6376, section 3.4.4:
//
// ""
// The "simple" body canonicalization algorithm ignores all empty lines
// at the end of the message body.  An empty line is a line of zero
// length after removal of the line terminator.  If there is no body or
// no trailing CRLF on the message body, a CRLF is added.  It makes no
// other changes to the message body.  In more formal terms, the
// "simple" body canonicalization algorithm converts "*CRLF" at the end
// of the body to a single "CRLF".
// ""
func newSimpleBody(r io.Reader) io.Reader {
	return &trimTrailingCRLFs{r: r}
}

// newRelaxedBody implements the "relaxed" Body Canonicalization Algorithm
// from RFC 6376, section 3.4.4.
//
// ""
// a.  Reduce whitespace:
//
//    *  Ignore all whitespace at the end of lines.  Implementations
//       MUST NOT remove the CRLF at the end of the line.
//
//    *  Reduce all sequences of WSP within a line to a single SP
//       character.
//
// b.  Ignore all empty lines at the end of the message body.  "Empty
//     line" is defined in Section 3.4.3.  If the body is non-empty but
//     does not end with a CRLF, a CRLF is added.
// ""
func newRelaxedBody(r io.Reader) io.Reader {
	return &trimTrailingCRLFs{r: &reduceWhitespace{r: r}}
}

// trimTrailingCRLFs is an io.Reader that trims any number of
// trailing CRLF values in the data being read to  a single CRLF.
type trimTrailingCRLFs struct {
	r io.Reader

	data [2048]byte
	off  int
	len  int
	rerr error

	inCR     bool // last byte was '\r'
	numCRLFs int  // last 2*numCRLFs bytes were CRLFs
	eiplogue bool // after all input processed, send a final CRLF
}

func (s *trimTrailingCRLFs) Read(buf []byte) (n int, err error) {
	for s.len == 0 {
		if s.rerr != nil {
			if !s.eiplogue {
				s.data[0], s.data[1] = '\r', '\n'
				s.off = 0
				s.len = 2
				s.eiplogue = true
				break
			}
			return n, s.rerr
		}
		s.off = 0
		s.len, s.rerr = s.r.Read(s.data[:])
	}

	if s.eiplogue {
		n = copy(buf, s.data[s.off:s.off+s.len])
		s.off += n
		s.len -= n
		return n, nil
	}

	n = 0
	for s.len > 0 && n < len(buf) {
		c := s.data[s.off]
		s.off++
		s.len--

		if c != '\n' {
			if s.inCR {
				buf[n] = '\r'
				n++
				s.inCR = false // bad CRLF
			}
		}

		switch c {
		case '\r':
			s.inCR = true
		case '\n':
			if s.inCR {
				s.numCRLFs++
				s.inCR = false
			} else {
				buf[n] = '\n'
				n++
			}
		default:
			for ; s.numCRLFs > 0 && n+1 < len(buf); s.numCRLFs-- {
				buf[n+0], buf[n+1] = '\r', '\n'
				n += 2
			}
			if s.numCRLFs > 0 {
				// We ran out of space in buf.
				// Keep the last s.data character.
				s.off--
				s.len++
				return n, nil
			}
			buf[n] = c
			n++
		}
	}

	return n, nil
}

// reduceWhitespace is an io.Reader that reduces any sequence
// of one of more ' ' or '\t' characters to a single ' '.
type reduceWhitespace struct {
	r io.Reader

	inWS bool // last byte was ' ' or '\t'
}

func (r *reduceWhitespace) Read(buf []byte) (n int, err error) {
	if len(buf) == 0 {
		return r.r.Read(buf) // pass on the err value
	}

	out := buf[:0]

	if r.inWS {
		buf = buf[1:] // leave space for whitespace
	}

	n, err = r.r.Read(buf)
	for _, c := range buf[:n] {
		switch c {
		case ' ', '\t':
			if r.inWS {
				continue
			} else {
				r.inWS = true
			}
		default:
			if r.inWS {
				out = append(out, ' ')
			}
			fallthrough
		case '\r', '\n':
			out = append(out, c)
			r.inWS = false
		}
	}

	return len(out), err
}
