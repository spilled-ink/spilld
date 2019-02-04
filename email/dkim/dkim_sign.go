// Package dkim implements DKIM message signing and verification.
package dkim

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// A Signer signs email with a DKIM-Signature.
type Signer struct {
	key *rsa.PrivateKey

	Domain   string   // d=, signing domain
	Selector string   // s=, key selector, TXT record is: <Selector>._domainkey.<Domain>
	Headers  []string // h=, list of headers in lower-case to sign
}

// NewSigner creates a Signer around a privateKey with prepopulated Headers.
// Set the Domain and Selector fields before using it.
func NewSigner(privateKey []byte) (*Signer, error) {
	headers := []string{
		"content-type",
		"date",
		"from",
		"in-reply-to",
		"message-id",
		"mime-version",
		"references",
		"subject",
		"to",
	}
	sort.Strings(headers)

	block, _ := pem.Decode(privateKey)
	if block == nil {
		return nil, errors.New("dkim: cannot decode key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("dkim: cannot parse key: %v", err)
	}

	return &Signer{
		Headers: headers,
		key:     key,
	}, nil
}

// Sign signs an email, reporting a new DKIM-Signature header.
// It is safe for use by multiple goroutines simultaneously.
func (s *Signer) Sign(hdr Header, body io.Reader) (dkimHeaderValue []byte, err error) {
	h := sha256.New()

	buf := bytes.NewBuffer(make([]byte, 0, 512))
	buf.WriteString("v=1; a=rsa-sha256; c=relaxed/relaxed; d=")
	buf.WriteString(s.Domain)
	buf.WriteString("; s=")
	buf.WriteString(s.Selector)
	buf.WriteString("; h=")
	if err := collectRelaxedHeaders(buf, h, s.Headers, hdr); err != nil {
		return nil, err
	}
	buf.WriteString("; bh=")
	if err := relaxedBodyHash(buf, body); err != nil {
		return nil, err
	}
	buf.WriteString("; b=")

	io.WriteString(h, "dkim-signature:")
	h.Write(buf.Bytes())

	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, h.Sum(nil))
	if err != nil {
		return nil, fmt.Errorf("dkim: %v", err)
	}
	sigFinal := make([]byte, base64.StdEncoding.EncodedLen(len(sig)))
	base64.StdEncoding.Encode(sigFinal, sig)

	// Add folding white space.
	// Valid as per RFC 4871, 3.5:
	// """
	//   b=  The signature data (base64; REQUIRED).  Whitespace is ignored in
	//       this value and MUST be ignored when reassembling the original
	//       signature.  In particular, the signing process can safely insert
	//       FWS in this value in arbitrary places to conform to line-length
	//       limits.
	// """
	for len(sigFinal) > 0 {
		n := len(sigFinal)
		if n > 66 {
			n = 66
		}
		buf.Write(sigFinal[:n])
		sigFinal = sigFinal[n:]
		if len(sigFinal) > 0 {
			buf.WriteByte(' ')
		}
	}
	return buf.Bytes(), nil
}

// Header is the set of MIME headers on the email being signed.
//
// The Get method is called by the signer with lower-case headers
// and it is the responsibility of the implementation to search
// its header names case-insensitively.
type Header interface {
	Get(header string) (value string)
}

func relaxedBodyHash(dst *bytes.Buffer, body io.Reader) error {
	var b [sha256.BlockSize]byte
	h := sha256.New()
	if _, err := io.Copy(h, newRelaxedBody(body)); err != nil {
		return fmt.Errorf("dkim: hashing body: %v", err)
	}
	w := base64.NewEncoder(base64.StdEncoding, dst)
	if _, err := w.Write(h.Sum(b[:0])); err != nil {
		return err
	}
	return w.Close()
}

func collectRelaxedHeaders(dstHeaderKeys *bytes.Buffer, dstHeaderBytes io.Writer, potentialHeaders []string, hdr Header) (err error) {
	oneByte := make([]byte, 1)
	numHeaders := 0
	for _, hdrKey := range potentialHeaders {
		v := hdr.Get(hdrKey)
		if v == "" {
			continue
		}
		if numHeaders > 0 {
			dstHeaderKeys.WriteByte(':')
		}
		numHeaders++
		dstHeaderKeys.WriteString(hdrKey)

		// RFC 6376
		// 3.4.2.1:
		// Convert all header field names (not the header field values) to
		// lowercase.  For example, convert "SUBJect: AbC" to "subject: AbC".
		if _, err := io.WriteString(dstHeaderBytes, hdrKey); err != nil {
			return err
		}
		// 3.4.2.2:
		// Header continuations are already unfolded in email.Header.
		//
		// 3.4.2.5:
		// Delete any WSP characters remaining before and after the colon
		// separating the header field name from the header field value.  The
		// colon separator MUST be retained.
		oneByte[0] = ':'
		if _, err := dstHeaderBytes.Write(oneByte); err != nil {
			return err
		}
		// 3.4.2.4:
		// Delete all WSP characters at the end of each unfolded header field
		// value.
		v = strings.TrimSpace(v)
		// 3.4.2.3:
		// Convert all sequences of one or more WSP characters to a single SP
		// character.  WSP characters here include those before and after a
		// line folding boundary.
		inWhitespace := false
		for i := 0; i < len(v); i++ {
			c := v[i]
			switch c {
			case ' ', '\t':
				if inWhitespace {
					continue
				}
				inWhitespace = true
				c = ' '
			default:
				inWhitespace = false
			}

			oneByte[0] = c
			if _, err := dstHeaderBytes.Write(oneByte); err != nil {
				return err
			}
		}
		if _, err := dstHeaderBytes.Write(crlf); err != nil {
			return err
		}
	}
	return nil
}
