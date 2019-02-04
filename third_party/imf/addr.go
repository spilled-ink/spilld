// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package imf

// Originally from go/src/net/mail/message.go.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/simplifiedchinese"
	"spilled.ink/email"
)

// Parses a single RFC 5322 address, e.g. "Barry Gibbs <bg@example.com>"
func ParseAddress(address string) (*email.Address, error) {
	return (&addrParser{s: address}).parseSingleAddress()
}

// ParseAddressList parses the given string as a list of addresses.
func ParseAddressList(list string) ([]*email.Address, error) {
	return (&addrParser{s: list}).parseAddressList()
}

// ParseReferences parses the "References:" header.
//
// Each Message-ID is fully decoded, then re-encoded as
// it is easier to work with Message-IDs encoded.
func ParseReferences(references string) (refs []string, err error) {
	refs, err = (&addrParser{s: references}).parseReferences()
	if err != nil {
		return nil, err
	}
	for i, ref := range refs {
		refs[i] = EncodeAddressSpec(ref)
	}
	return refs, nil
}

// ParseMessageID parses an encoded Message-ID.
// This is the form used in the "Message-ID:" and "In-Reply-To:" headers.
//
// On success, this function returns the original input.
func ParseReference(reference string) (string, error) {
	refs, err := (&addrParser{s: reference}).parseReferences()
	if err != nil {
		return "", err
	}
	if len(refs) != 1 {
		return "", errors.New("mail: more than one message id found")
	}
	return EncodeAddressSpec(refs[0]), nil
}

// FormatAddress formats the address as a valid RFC 5322 address.
// If the address's name contains non-ASCII characters
// the name will be rendered according to RFC 2047.
func FormatAddress(a *email.Address) string {
	s := EncodeAddressSpec(a.Addr)

	if a.Name == "" {
		return s
	}

	// If every character is printable ASCII, quoting is simple.
	allPrintable := true
	for _, r := range a.Name {
		// isWSP here should actually be isFWS,
		// but we don't support folding yet.
		if !isVchar(r) && !isWSP(r) || isMultibyte(r) {
			allPrintable = false
			break
		}
	}
	if allPrintable {
		return quoteString(a.Name) + " " + s
	}

	// Text in an encoded-word in a display-name must not contain certain
	// characters like quotes or parentheses (see RFC 2047 section 5.3).
	// When this is the case encode the name using base64 encoding.
	if strings.ContainsAny(a.Name, "\"#$%&'(),.:;<>@[]^`{|}~") {
		return mime.BEncoding.Encode("utf-8", a.Name) + " " + s
	}
	return mime.QEncoding.Encode("utf-8", a.Name) + " " + s
}

func FormatAddressList(list []email.Address) string {
	var addrs []string
	for _, a := range list {
		addrs = append(addrs, FormatAddress(&a))
	}
	return strings.Join(addrs, ", ")
}

func EncodeAddressSpec(address string) string {
	at := strings.LastIndex(address, "@")
	var local, domain string
	if at < 0 {
		// This is a malformed address ("@" is required in addr-spec);
		// treat the whole address as local-part.
		local = address
	} else {
		local, domain = address[:at], address[at+1:]
	}

	// Add quotes if needed
	quoteLocal := false
	for i, r := range local {
		if isAtext(r, false, false) {
			continue
		}
		if r == '.' {
			// Dots are okay if they are surrounded by atext.
			// We only need to check that the previous byte is
			// not a dot, and this isn't the end of the string.
			if i > 0 && local[i-1] != '.' && i < len(local)-1 {
				continue
			}
		}
		quoteLocal = true
		break
	}
	if quoteLocal {
		local = quoteString(local)

	}

	return "<" + local + "@" + domain + ">"
}

type addrParser struct {
	s string
}

func (p *addrParser) parseAddressList() ([]*email.Address, error) {
	var list []*email.Address
	for {
		p.skipSpace()
		addrs, err := p.parseAddress(true)
		if err != nil {
			return nil, err
		}
		list = append(list, addrs...)

		if !p.skipCFWS() {
			return nil, errors.New("mail: misformatted parenthetical comment")
		}
		if p.empty() {
			break
		}
		if !p.consume(',') {
			return nil, errors.New("mail: expected comma")
		}
	}
	return list, nil
}

func (p *addrParser) parseSingleAddress() (*email.Address, error) {
	addrs, err := p.parseAddress(true)
	if err != nil {
		return nil, err
	}
	if !p.skipCFWS() {
		return nil, errors.New("mail: misformatted parenthetical comment")
	}
	if !p.empty() {
		return nil, fmt.Errorf("mail: expected single address, got %q", p.s)
	}
	if len(addrs) == 0 {
		return nil, errors.New("mail: empty group")
	}
	if len(addrs) > 1 {
		return nil, errors.New("mail: group with multiple addresses")
	}
	return addrs[0], nil
}

// parseAddress parses a single RFC 5322 address at the start of p.
func (p *addrParser) parseAddress(handleGroup bool) ([]*email.Address, error) {
	p.skipSpace()
	if p.empty() {
		return nil, errors.New("mail: no address")
	}

	// address = mailbox / group
	// mailbox = name-addr / addr-spec
	// group = display-name ":" [group-list] ";" [CFWS]

	// addr-spec has a more restricted grammar than name-addr,
	// so try parsing it first, and fallback to name-addr.
	// TODO(dsymonds): Is this really correct?
	spec, err := p.consumeAddrSpec()
	if err == nil {
		var displayName string
		p.skipSpace()
		if !p.empty() && p.peek() == '(' {
			displayName, err = p.consumeDisplayNameComment()
			if err != nil {
				return nil, err
			}
		}

		return []*email.Address{{
			Name: displayName,
			Addr: spec,
		}}, err
	}

	// display-name
	var displayName string
	if p.peek() != '<' {
		displayName, err = p.consumePhrase()
		if err != nil {
			return nil, err
		}
	}

	p.skipSpace()
	if handleGroup {
		if p.consume(':') {
			return p.consumeGroupList()
		}
	}
	// angle-addr = "<" addr-spec ">"
	if !p.consume('<') {
		return nil, errors.New("mail: no angle-addr")
	}
	spec, err = p.consumeAddrSpec()
	if err != nil {
		return nil, err
	}
	if !p.consume('>') {
		return nil, errors.New("mail: unclosed angle-addr")
	}

	return []*email.Address{{
		Name: displayName,
		Addr: spec,
	}}, nil
}

// parseReferences parses the "References:" header.
//
// It consists of a sequence of '<' addr-spec '>' separated by
// a single space. https://cr.yp.to/immhf/thread.html#references
func (p *addrParser) parseReferences() (refs []string, err error) {
	for {
		if p.empty() {
			break
		}
		if !p.consume('<') {
			return nil, errors.New("imf: references: no angle-addr")
		}
		spec, err := p.consumeAddrSpec()
		if err != nil {
			return nil, err
		}
		refs = append(refs, spec)
		if !p.consume('>') {
			return nil, errors.New("mail: unclosed angle-addr")
		}
		p.skipSpace()
	}
	return refs, nil
}

func (p *addrParser) consumeGroupList() ([]*email.Address, error) {
	var group []*email.Address
	// handle empty group.
	p.skipSpace()
	if p.consume(';') {
		p.skipCFWS()
		return group, nil
	}

	for {
		p.skipSpace()
		// embedded groups not allowed.
		addrs, err := p.parseAddress(false)
		if err != nil {
			return nil, err
		}
		group = append(group, addrs...)

		if !p.skipCFWS() {
			return nil, errors.New("mail: misformatted parenthetical comment")
		}
		if p.consume(';') {
			p.skipCFWS()
			break
		}
		if !p.consume(',') {
			return nil, errors.New("mail: expected comma")
		}
	}
	return group, nil
}

// consumeAddrSpec parses a single RFC 5322 addr-spec at the start of p.
func (p *addrParser) consumeAddrSpec() (spec string, err error) {
	orig := *p
	defer func() {
		if err != nil {
			*p = orig
		}
	}()

	// local-part = dot-atom / quoted-string
	var localPart string
	p.skipSpace()
	if p.empty() {
		return "", errors.New("mail: no addr-spec")
	}
	if p.peek() == '"' {
		// quoted-string
		localPart, err = p.consumeQuotedString()
		if localPart == "" {
			err = errors.New("mail: empty quoted string in addr-spec")
		}
	} else {
		// dot-atom
		localPart, err = p.consumeAtom(true, false)
	}
	if err != nil {
		return "", err
	}

	if !p.consume('@') {
		return "", errors.New("mail: missing @ in addr-spec")
	}

	// domain = dot-atom / domain-literal
	var domain string
	p.skipSpace()
	if p.empty() {
		return "", errors.New("mail: no domain in addr-spec")
	}
	// TODO(dsymonds): Handle domain-literal
	domain, err = p.consumeAtom(true, false)
	if err != nil {
		return "", err
	}

	return localPart + "@" + domain, nil
}

// consumePhrase parses the RFC 5322 phrase at the start of p.
func (p *addrParser) consumePhrase() (phrase string, err error) {
	// phrase = 1*word
	var words []string
	var isPrevEncoded bool
	for {
		// word = atom / quoted-string
		var word string
		p.skipSpace()
		if p.empty() {
			break
		}
		isEncoded := false
		if p.peek() == '"' {
			// quoted-string
			word, err = p.consumeQuotedString()
		} else {
			// atom
			// We actually parse dot-atom here to be more permissive
			// than what RFC 5322 specifies.
			word, err = p.consumeAtom(true, true)
			if err == nil {
				word, isEncoded, err = p.decodeRFC2047Word(word)
			}
		}

		if err != nil {
			break
		}
		if isPrevEncoded && isEncoded {
			words[len(words)-1] += word
		} else {
			words = append(words, word)
		}
		isPrevEncoded = isEncoded
	}
	// Ignore any error if we got at least one word.
	if err != nil && len(words) == 0 {
		return "", fmt.Errorf("mail: missing word in phrase: %v", err)
	}
	phrase = strings.Join(words, " ")
	return phrase, nil
}

// consumeQuotedString parses the quoted string at the start of p.
func (p *addrParser) consumeQuotedString() (qs string, err error) {
	// Assume first byte is '"'.
	i := 1
	qsb := make([]rune, 0, 10)

	escaped := false

Loop:
	for {
		r, size := utf8.DecodeRuneInString(p.s[i:])

		switch {
		case size == 0:
			return "", errors.New("mail: unclosed quoted-string")

		case size == 1 && r == utf8.RuneError:
			return "", fmt.Errorf("mail: invalid utf-8 in quoted-string: %q", p.s)

		case escaped:
			//  quoted-pair = ("\" (VCHAR / WSP))

			if !isVchar(r) && !isWSP(r) {
				return "", fmt.Errorf("mail: bad character in quoted-string: %q", r)
			}

			qsb = append(qsb, r)
			escaped = false

		case isQtext(r) || isWSP(r):
			// qtext (printable US-ASCII excluding " and \), or
			// FWS (almost; we're ignoring CRLF)
			qsb = append(qsb, r)

		case r == '"':
			break Loop

		case r == '\\':
			escaped = true

		default:
			return "", fmt.Errorf("mail: bad character in quoted-string: %q", r)

		}

		i += size
	}
	p.s = p.s[i+1:]
	return string(qsb), nil
}

// consumeAtom parses an RFC 5322 atom at the start of p.
// If dot is true, consumeAtom parses an RFC 5322 dot-atom instead.
// If permissive is true, consumeAtom will not fail on:
// - leading/trailing/double dots in the atom (see golang.org/issue/4938)
// - special characters (RFC 5322 3.2.3) except '<', '>', ':' and '"' (see golang.org/issue/21018)
func (p *addrParser) consumeAtom(dot bool, permissive bool) (atom string, err error) {
	i := 0

Loop:
	for {
		r, size := utf8.DecodeRuneInString(p.s[i:])
		switch {
		case size == 1 && r == utf8.RuneError:
			return "", fmt.Errorf("mail: invalid utf-8 in address: %q", p.s)

		case size == 0 || !isAtext(r, dot, permissive):
			break Loop

		default:
			i += size

		}
	}

	if i == 0 {
		return "", errors.New("mail: invalid string")
	}
	atom, p.s = p.s[:i], p.s[i:]
	if !permissive {
		if strings.HasPrefix(atom, ".") {
			return "", errors.New("mail: leading dot in atom")
		}
		if strings.Contains(atom, "..") {
			return "", errors.New("mail: double dot in atom")
		}
		if strings.HasSuffix(atom, ".") {
			return "", errors.New("mail: trailing dot in atom")
		}
	}
	return atom, nil
}

func (p *addrParser) consumeDisplayNameComment() (string, error) {
	if !p.consume('(') {
		return "", errors.New("mail: comment does not start with (")
	}
	comment, ok := p.consumeComment()
	if !ok {
		return "", errors.New("mail: misformatted parenthetical comment")
	}

	// TODO(stapelberg): parse quoted-string within comment
	words := strings.FieldsFunc(comment, func(r rune) bool { return r == ' ' || r == '\t' })
	for idx, word := range words {
		decoded, isEncoded, err := p.decodeRFC2047Word(word)
		if err != nil {
			return "", err
		}
		if isEncoded {
			words[idx] = decoded
		}
	}

	return strings.Join(words, " "), nil
}

func (p *addrParser) consume(c byte) bool {
	if p.empty() || p.peek() != c {
		return false
	}
	p.s = p.s[1:]
	return true
}

// skipSpace skips the leading space and tab characters.
func (p *addrParser) skipSpace() {
	p.s = strings.TrimLeft(p.s, " \t")
}

func (p *addrParser) peek() byte {
	return p.s[0]
}

func (p *addrParser) empty() bool {
	return p.len() == 0
}

func (p *addrParser) len() int {
	return len(p.s)
}

// skipCFWS skips CFWS as defined in RFC5322.
func (p *addrParser) skipCFWS() bool {
	p.skipSpace()

	for {
		if !p.consume('(') {
			break
		}

		if _, ok := p.consumeComment(); !ok {
			return false
		}

		p.skipSpace()
	}

	return true
}

func (p *addrParser) consumeComment() (string, bool) {
	// '(' already consumed.
	depth := 1

	var comment string
	for {
		if p.empty() || depth == 0 {
			break
		}

		if p.peek() == '\\' && p.len() > 1 {
			p.s = p.s[1:]
		} else if p.peek() == '(' {
			depth++
		} else if p.peek() == ')' {
			depth--
		}
		if depth > 0 {
			comment += p.s[:1]
		}
		p.s = p.s[1:]
	}

	return comment, depth == 0
}

func (p *addrParser) decodeRFC2047Word(s string) (word string, isEncoded bool, err error) {
	word, err = mimeDecoder.Decode(s)
	if err == nil {
		return word, true, nil
	}

	if _, ok := err.(charsetError); ok {
		return s, true, err
	}

	// Ignore invalid RFC 2047 encoded-word errors.
	return s, false, nil
}

type charsetError string

func (e charsetError) Error() string {
	return fmt.Sprintf("charset not supported: %q", string(e))
}

// isAtext reports whether r is an RFC 5322 atext character.
// If dot is true, period is included.
// If permissive is true, RFC 5322 3.2.3 specials is included,
// except '<', '>', ':' and '"'.
func isAtext(r rune, dot, permissive bool) bool {
	switch r {
	case '.':
		return dot

	// RFC 5322 3.2.3. specials
	case '(', ')', '[', ']', ';', '@', '\\', ',':
		return permissive

	case '<', '>', '"', ':':
		return false
	}
	return isVchar(r)
}

// isQtext reports whether r is an RFC 5322 qtext character.
func isQtext(r rune) bool {
	// Printable US-ASCII, excluding backslash or quote.
	if r == '\\' || r == '"' {
		return false
	}
	return isVchar(r)
}

// quoteString renders a string as an RFC 5322 quoted-string.
func quoteString(s string) string {
	var buf bytes.Buffer
	buf.WriteByte('"')
	for _, r := range s {
		if isQtext(r) || isWSP(r) {
			buf.WriteRune(r)
		} else if isVchar(r) {
			buf.WriteByte('\\')
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return buf.String()
}

// isVchar reports whether r is an RFC 5322 VCHAR character.
func isVchar(r rune) bool {
	// Visible (printing) characters.
	return '!' <= r && r <= '~' || isMultibyte(r)
}

// isMultibyte reports whether r is a multi-byte UTF-8 character
// as supported by RFC 6532
func isMultibyte(r rune) bool {
	return r >= utf8.RuneSelf
}

// isWSP reports whether r is a WSP (white space).
// WSP is a space or horizontal tab (RFC 5234 Appendix B).
func isWSP(r rune) bool {
	return r == ' ' || r == '\t'
}

var mimeDecoder = &mime.WordDecoder{
	CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		encoding, err := ianaindex.MIME.Encoding(charset)
		if err != nil {
			return nil, err
		}
		if encoding == nil {
			if charset == "gb2312" {
				encoding = simplifiedchinese.HZGB2312
			} else {
				log.Printf("no encoding for charset: %q", charset)
				return input, nil
			}
		}
		return encoding.NewDecoder().Reader(input), nil
	},
}
