package imapparser

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"crawshaw.io/iox"
)

type Token int

const (
	TokenUnknown Token = iota
	TokenAtom
	TokenNumber
	TokenString
	TokenLiteral
	TokenListStart
	TokenListEnd
	TokenNIL
	TokenFlag
	TokenSequences // sequence-set
	TokenTag
	TokenSearchKey // either atom, sequence-set, '(', or ')'
	TokenFetchItem
	TokenDate
	TokenListMailbox
	TokenEnd
)

func (t Token) String() string {
	switch t {
	case TokenUnknown:
		return "unknown-token"
	case TokenAtom:
		return "atom"
	case TokenNumber:
		return "number"
	case TokenString:
		return "astring"
	case TokenLiteral:
		return "literal"
	case TokenListStart:
		return "list-start"
	case TokenListEnd:
		return "list-end"
	case TokenNIL:
		return "NIL"
	case TokenFlag:
		return "flag"
	case TokenSequences:
		return "sequences"
	case TokenTag:
		return "tag"
	case TokenEnd:
		return "end"
	case TokenSearchKey:
		return "search-key"
	case TokenFetchItem:
		return "fetch-item"
	case TokenDate:
		return "date"
	case TokenListMailbox:
		return "list-mailbox"
	default:
		return fmt.Sprintf("Token(%d)", int(t))
	}
}

// Scanner tokenizes IMAP commands.
//
// A note on whitespace. RFC 3501 section 9 says:
//
//        (2) In all cases, SP refers to exactly one space.  It is
//        NOT permitted to substitute TAB, insert additional spaces,
//        or otherwise treat SP as being equivalent to LWSP.
//
// While the specification is strict, this parser is lenient.
// Any number of spaces or tabs will be consumed before a token.
type Scanner struct {
	buf         *bufio.Reader
	ioErr       error
	listDepth   int
	lastWasCRLF bool

	ContFn func(msg string, len uint32)

	Error     error
	Token     Token
	Value     []byte
	Sequences []SeqRange
	FetchItem FetchItem
	Date      time.Time
	Number    uint64
	Literal   *iox.BufferFile
}

func NewScanner(r *bufio.Reader, literalBuf *iox.BufferFile, contFn func(msg string, len uint32)) *Scanner {
	return &Scanner{
		buf:     r,
		ContFn:  contFn,
		Literal: literalBuf,
	}
}

func (s *Scanner) SetSource(r *bufio.Reader) {
	s.buf = r
}

// peekChar reports the next byte without consuming it.
// The byte can be any value from 0x01-0xff. NUL is an error.
// This is the CHAR8 rule from RFC 3501.
//
// On error peekChar reports 0 and sets ioErr.
func (s *Scanner) peekChar() byte {
	if s.ioErr != nil {
		return 0
	}
	b, err := s.buf.Peek(1)
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			err = io.EOF // COMPRESS generates unexpected EOF
		}
		s.ioErr = err
		return 0
	}
	if b[0] == 0 {
		s.ioErr = fmt.Errorf("imapparser: unexpected NUL")
	}
	return b[0]
}

// readChar reports the next byte.
// The byte can be any value from 0x01-0xff. NUL is an error.
// This is the CHAR8 rule from RFC 3501.
//
// On error readChar reports 0 and sets ioErr.
func (s *Scanner) readChar() byte {
	if s.ioErr != nil {
		return 0
	}
	b, err := s.buf.ReadByte()
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			err = io.EOF // COMPRESS generates unexpected EOF
		}
		s.ioErr = err
		return 0
	}
	if b == 0 {
		s.ioErr = fmt.Errorf("imapparser: unexpected NUL")
	}
	return b
}

var (
	errUnterminatedString = errors.New("imapparser: unterminated string")
)

func (s *Scanner) readQuotedString() bool {
	s.readChar() // consume initial '"'
	for {
		b := s.readChar()
		switch b {
		case 0:
			if s.ioErr == io.EOF {
				s.Error = errUnterminatedString
			} else {
				s.Error = s.ioErr
			}
			return false
		case '"':
			return true
		case '\r', '\n':
			s.Error = fmt.Errorf("imapparser: invalid character in quoted string: %q", string(b))
			return false
		case '\\':
			b = s.readChar()
			switch b {
			case 0:
				if s.ioErr == io.EOF {
					s.Error = errUnterminatedString
				} else {
					s.Error = s.ioErr
				}
				return false
			case '\\', '"':
				s.Value = append(s.Value, b)
			default:
				s.Error = fmt.Errorf("imapserver: invalid escape character in quoted string: %q", string(b))
				return false
			}
		default:
			s.Value = append(s.Value, b)
		}
	}
}

// readAtom reads an IMAP atom.
//
// Condensed grammar from RFC 3501 section 9:
//
//	atom            = 1*<any 7-bit printable except atom-specials>
//
//	atom-specials   = "(" / ")" / "{" / SP / CTL / "%" / "*" / " / "\"
func (s *Scanner) readAtom() bool {
	oldlen := len(s.Value)
loop:
	for {
		b := s.peekChar()
		switch b {
		case 0:
			s.readChar()
			break loop
		case ' ', '\r', '\n', ')':
			break loop
		case '(', '{', '%', '*', ']':
			if len(s.Value) > oldlen {
				s.Error = fmt.Errorf("imapparser: invalid atom character: %q", string(b))
			}
			return false
		default:
			if !is7bitPrint(b) {
				if len(s.Value) > oldlen {
					s.Error = fmt.Errorf("imapparser: invalid atom character: %q", string(b))
				}
				return false
			}
			s.readChar()
			s.Value = append(s.Value, b)
		}
	}
	return len(s.Value) > oldlen
}

// readTag reads an IMAP tag.
//
// Condensed grammar from RFC 3501 section 9:
//
// 	tag             = 1*<any 7-bit printable except tag-specials>
//
//	tag-specials   = "(" / ")" / "{" / SP / CTL / "%" / "*" / " / "\" / "+"
func (s *Scanner) readTag() bool {
	oldlen := len(s.Value)
loop:
	for {
		b := s.peekChar()
		switch b {
		case 0:
			s.readChar()
			break loop
		case ' ', '\r', '\n':
			break loop
		case '(', ')', '{', '%', '*', '"', '\\', '+':
			if len(s.Value) > oldlen {
				s.Error = fmt.Errorf("imapparser: invalid tag character: %q", string(b))
			}
			return false
		default:
			if !is7bitPrint(b) {
				if len(s.Value) > oldlen {
					s.Error = fmt.Errorf("imapparser: invalid tag character: %q", string(b))
				}
				return false
			}
			s.readChar()
			s.Value = append(s.Value, b)
		}
	}
	return len(s.Value) > oldlen
}

// astring         = 1*ASTRING-CHAR / string
//
// ASTRING-CHAR   = ATOM-CHAR
//
// atom            = 1*ATOM-CHAR
//
// ATOM-CHAR       = <any CHAR except astring-specials>
//
// astring-specials   = "(" / ")" / "{" / SP / CTL / "%" / "*" /
//                   DQUOTE / "\"
//
// string          = quoted / literal
func (s *Scanner) readAstring() bool {
	b := s.peekChar()

	switch b {
	case 0:
		return false
	case '"':
		return s.readQuotedString()
	case '{':
		return s.readLiteral(1024)
	}

	oldlen := len(s.Value)
loop:
	for {
		b := s.peekChar()
		switch b {
		case 0:
			s.readChar()
			break loop
		case ' ', '\r', '\n':
			break loop
		case '(', ')', '{', '%', '*', '"', '\\':
			break loop
		default:
			if !is7bitPrint(b) {
				if len(s.Value) > oldlen {
					s.Error = fmt.Errorf("imapparser: invalid astring character: %q", string(b))
				}
				return false
			}
			s.readChar()
			s.Value = append(s.Value, b)
		}
	}
	return len(s.Value) > oldlen
}

// readListMailbox reads an IMAP list-mailbox.
// This is an astring that also allows % and *.
//
// list-mailbox    = 1*list-char / string
//
// list-char       = ATOM-CHAR / list-wildcards / resp-specials
//
// list-wildcards  = "%" / "*"
func (s *Scanner) readListMailbox() bool {
	b := s.peekChar()

	switch b {
	case 0:
		return false
	case '"':
		return s.readQuotedString()
	case '{':
		return s.readLiteral(1024)
	}

	oldlen := len(s.Value)
loop:
	for {
		b := s.peekChar()
		switch b {
		case 0:
			s.readChar()
			break loop
		case ' ', '\r', '\n':
			break loop
		case '(', ')', '{', '"', '\\':
			break loop
		default:
			if !is7bitPrint(b) {
				if len(s.Value) > oldlen {
					s.Error = fmt.Errorf("imapparser: invalid astring character: %q", string(b))
				}
				return false
			}
			s.readChar()
			s.Value = append(s.Value, b)
		}
	}
	return len(s.Value) > oldlen
}

// is7bitPrint reports whether b is a printable 7-bit ASCII character.
//
// RFC 3501 states that "Characters are 7-bit US-ASCII unless otherwise specified."
func is7bitPrint(b byte) bool {
	return b >= 0x20 && b <= 0x7e
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func asciiUpper(buf []byte) {
	for i, b := range buf {
		if 'a' <= b && b <= 'z' {
			buf[i] = 'A' + b - 'a'
		}
	}
}

// readFlag reads an IMAP flag.
func (s *Scanner) readFlag() bool {
	b := s.peekChar()
	if b == '\\' {
		s.readChar()
		s.Value = append(s.Value, '\\')
		if !s.readAtom() {
			s.Value = s.Value[:0]
			s.Error = fmt.Errorf("imapparser: invalid flag: \"%s\"", string(s.peekChar()))
			return false
		}
		switch string(s.Value) {
		case `\Answered`, `\Flagged`, `\Deleted`, `\Seen`, `\Draft`:
			return true
		}
		s.Error = fmt.Errorf("imapparser: invalid flag: %q", string(s.Value))
		s.Value = s.Value[:0]
		return false
	}
	return s.readAtom()
}

// readSeqNumber reads an IMAP seq-number.
//
// From RFC 3501 section 9:
//
//	nz-number       = digit-nz *DIGIT
//		; Non-zero unsigned 32-bit integer
//		; (0 < n < 4,294,967,296)
//
//	seq-number      = nz-number / "*"
func (s *Scanner) readSeqNumber() (uint32, bool) {
	switch s.peekChar() {
	case 0:
		return 0, false
	case '*':
		s.readChar()
		return 0, true
	}

	v, err := s.readUint32()
	if err != nil {
		s.Error = err
		return 0, false
	}
	if v == 0 {
		s.Error = errors.New("imapparser: invalid seq-number: '0'")
		return 0, false
	}
	return uint32(v), true
}

func (s *Scanner) readUint32() (uint32, error) {
	var bufarr [11]byte // 1-byte more than base-10 uint32 to detect overflow
	buf := bufarr[:0]
	for {
		b := s.peekChar()
		if b == 0 || b < '0' || b > '9' {
			break
		}
		s.readChar()
		if len(buf) < cap(buf) {
			buf = append(buf, b)
		}
	}

	v, err := strconv.ParseUint(string(buf), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

func (s *Scanner) readUint64() (uint64, error) {
	var bufarr [20]byte
	buf := bufarr[:0]
	for {
		b := s.peekChar()
		if b == 0 || b < '0' || b > '9' {
			break
		}
		s.readChar()
		if len(buf) < cap(buf) {
			buf = append(buf, b)
		}
	}

	v, err := strconv.ParseUint(string(buf), 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func (s *Scanner) readListStart() bool {
	b := s.peekChar()
	if b == '(' {
		s.readChar()
		s.listDepth++
		return true
	}
	return false
}

func (s *Scanner) readListEnd() (bool, error) {
	b := s.peekChar()
	if b == ')' {
		s.readChar()
		if s.listDepth == 0 {
			return false, fmt.Errorf("imapparser: unbalanced list-end paren")
		}
		s.listDepth--
		return true, nil
	}
	return false, nil
}

// readSequence reads a single component of an IMAP sequence-set.
//
// In the RFC 3501 section 9 formal grammar that is:
//
//	(seq-number / seq-range)
//
// where
//
//	seq-range       = seq-number ":" seq-number
func (s *Scanner) readSequence() bool {
	min, found := s.readSeqNumber()
	if !found {
		return false
	}
	if s.peekChar() != ':' {
		s.Sequences = append(s.Sequences, SeqRange{Min: min, Max: min})
		return true
	}
	s.readChar() // consume ':'

	max, found := s.readSeqNumber()
	if !found {
		s.Error = errors.New("imapparser: missing upper value of seq-number")
		return false
	}
	if max < min && max != 0 {
		min, max = max, min // normalize SeqRange
	}
	s.Sequences = append(s.Sequences, SeqRange{Min: min, Max: max})
	return true
}

// readSequences reads an IMAP sequence-set.
//
// From RFC 3501 section 9:
//
//	sequence-set    = (seq-number / seq-range) *("," sequence-set)
func (s *Scanner) readSequences() bool {
	for {
		if !s.readSequence() {
			break
		}
		if s.peekChar() != ',' {
			break
		}
		s.readChar()
	}

	return len(s.Sequences) > 0
}

func (s *Scanner) consumeWhitespace() {
	for {
		b := s.peekChar()
		if b != ' ' && b != '\t' {
			return
		}
		s.readChar()
	}
}

func (s *Scanner) clear() {
	s.lastWasCRLF = false
	s.Token = TokenUnknown
	s.Value = s.Value[:0]
	s.Sequences = s.Sequences[:0]
	s.FetchItem.reset()
	s.Date = time.Time{}
	if s.Literal != nil {
		if err := s.Literal.Truncate(0); err != nil {
			panic(err)
		}
		if _, err := s.Literal.Seek(0, 0); err != nil {
			panic(err)
		}
	}
}

func (s *Scanner) Drain() {
	if s.lastWasCRLF {
		return
	}
	for {
		if _, err := s.buf.ReadSlice('\n'); err != bufio.ErrBufferFull {
			break
		}
	}
}

func (s *Scanner) Next(expect Token) bool {
	return s.next(expect, false)
}

func (s *Scanner) NextOrEnd(expect Token) bool {
	return s.next(expect, true)
}

func (s *Scanner) readLiteral(limit int) bool {
	// "{<digits>}CRLF<n bytes>"
	if s.peekChar() != '{' {
		return false
	}
	s.readChar()
	v, err := s.readUint32()
	if err != nil {
		s.Error = err
		return false
	}
	if b := s.readChar(); b != '}' {
		s.Error = fmt.Errorf("imapparser: bad literal, got %q instead of \"}\"", b)
		return false
	}
	if b := s.readChar(); b != '\r' {
		s.Error = fmt.Errorf("imapparser: bad literal, got %q instead of \\r", b)
		return false
	}
	if b := s.readChar(); b != '\n' {
		s.Error = fmt.Errorf("imapparser: bad literal, got %q instead of \\n", b)
		return false
	}

	if s.ContFn != nil {
		s.ContFn("+ Ready for additional text\r\n", v)
	}

	if v := int(v); limit != 0 {
		if v > limit {
			s.Error = fmt.Errorf("imapparser: literal length %d is greater than max %d", v, limit)
			return false
		}
		if cap(s.Value) > v {
			s.Value = s.Value[:v]
		} else {
			s.Value = append(s.Value[:0], make([]byte, v)...)
		}
		if _, err := io.ReadFull(s.buf, s.Value); err != nil {
			s.Value = s.Value[:0]
			s.Error = err
			return false
		}
		return true
	}

	if _, err := io.CopyN(s.Literal, s.buf, int64(v)); err != nil {
		s.Literal.Truncate(0)
		s.Literal.Seek(0, 0)
		s.Error = err
		return false
	}
	if _, err := s.Literal.Seek(0, 0); err != nil {
		s.Literal.Truncate(0)
		s.Error = err
		return false
	}
	return true
}

// readAlphanumeric reads a string of [A-Z0-9\.] characters.
//
// This is not formally defined in the IMAP grammar but is
// useful in parsing parts of the fetch item "section".
func (s *Scanner) readAlphanumeric() bool {
	oldlen := len(s.Value)
	for {
		b := s.peekChar()
		if ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9') || b == '.' {
			s.Value = append(s.Value, b)
			s.readChar()
			continue
		}
		break
	}
	return len(s.Value) > oldlen
}

// readDate scans a date.
func (s *Scanner) readDate() bool {
	quoted := false
	b := s.peekChar()
	if b == '"' {
		s.readChar()
		quoted = true
	}
	day, err := s.readUint32()
	if err != nil {
		s.Error = err
		return false
	}
	if day > 31 {
		s.Error = fmt.Errorf("invalid day: %d", day)
		return false
	}

	b = s.peekChar()
	if b != '-' {
		s.Error = errors.New("invalid date")
		return false
	}
	s.readChar()

	var month [3]byte
	month[0] = s.readChar()
	month[1] = s.readChar()
	month[2] = s.readChar()
	asciiUpper(month[:])

	var m time.Month
	switch string(month[:]) {
	case "JAN":
		m = time.January
	case "FEB":
		m = time.February
	case "MAR":
		m = time.March
	case "APR":
		m = time.April
	case "MAY":
		m = time.May
	case "JUN":
		m = time.June
	case "JUL":
		m = time.July
	case "AUG":
		m = time.August
	case "SEP":
		m = time.September
	case "OCT":
		m = time.October
	case "NOV":
		m = time.November
	case "DEC":
		m = time.December
	default:
		s.Error = fmt.Errorf("invalid month: %q", month[:])
		return false
	}

	b = s.peekChar()
	if b != '-' {
		s.Error = errors.New("invalid date")
		return false
	}
	s.readChar()

	year, err := s.readUint32()
	if err != nil {
		s.Error = err
		return false
	}
	if year > 9999 {
		s.Error = fmt.Errorf("invalid year: %d", year)
		return false
	}

	if quoted {
		if s.readChar() != '"' {
			s.Error = fmt.Errorf("date missing end quote")
			return false
		}
	}

	s.Date = time.Date(int(year), m, int(day), 0, 0, 0, 0, time.UTC)
	return true
}

// readFetchItem scans a fetch-att.
func (s *Scanner) readFetchItem() bool {
	if !s.readAlphanumeric() {
		return false
	}

	item := &s.FetchItem
	switch string(s.Value) {
	case "ALL":
		item.Type = FetchAll
	case "FAST":
		item.Type = FetchFast
	case "FULL":
		item.Type = FetchFull
	case "ENVELOPE":
		item.Type = FetchEnvelope
	case "FLAGS":
		item.Type = FetchFlags
	case "INTERNALDATE":
		item.Type = FetchInternalDate
	case "RFC822.HEADER":
		item.Type = FetchRFC822Header
	case "RFC822.SIZE":
		item.Type = FetchRFC822Size
	case "RFC822.TEXT":
		item.Type = FetchRFC822Text
	case "UID":
		item.Type = FetchUID
	case "MODSEQ":
		item.Type = FetchModSeq
	case "BODYSTRUCTURE":
		item.Type = FetchBodyStructure
	case "BODY":
		item.Type = FetchBody
	case "BODY.PEEK":
		item.Type = FetchBody
		item.Peek = true
	default:
		s.Error = errors.New("imapparser: FETCH unknown item")
		return false
	}
	s.Value = s.Value[:0]

	if s.peekChar() != '[' {
		s.consumeWhitespace()
		return true
	}

	// A section follows.
	if item.Type != FetchBody {
		s.Error = errors.New("imapparser: FETCH item unexpected section")
		return false
	}
	s.readChar() // consume '['
	section := &item.Section

	// Read numeric path.
	for {
		if !isDigit(s.peekChar()) {
			break
		}
		v, err := s.readUint32()
		if err != nil {
			s.Error = errors.New("imapparser: FETCH item bad numeric path")
			return false
		}
		if v >= 1<<16 {
			s.Error = errors.New("imapparser: FETCH item path number too big")
			return false
		}
		section.Path = append(section.Path, uint16(v))

		if s.peekChar() == '.' {
			s.readChar()
		}
	}

	if s.readAlphanumeric() {
		switch string(s.Value) {
		case "HEADER":
			section.Name = "HEADER"
		case "HEADER.FIELDS":
			section.Name = "HEADER.FIELDS"
		case "HEADER.FIELDS.NOT":
			section.Name = "HEADER.FIELDS.NOT"
		case "TEXT":
			section.Name = "TEXT"
		case "MIME":
			if len(section.Path) == 0 {
				s.Error = errors.New("imapparser: FETCH item invalid section name")
				return false
			}
			section.Name = "MIME"
		default:
			s.Error = errors.New("imapparser: FETCH item invalid section name")
			return false
		}
		s.Value = s.Value[:0]

		if strings.HasPrefix(section.Name, "HEADER.FIELDS") {
			s.consumeWhitespace()
			// read header-list
			if s.peekChar() != '(' {
				s.Error = errors.New("imapparser: FETCH item missing header-list")
				return false
			}
			s.readChar() // consume '('

			for {
				s.consumeWhitespace()
				s.Value = s.Value[:0]
				if !s.readAstring() {
					break
				}
				section.Headers = appendValue(section.Headers, s.Value)
			}

			if s.peekChar() != ')' {
				s.Error = errors.New("imapparser: FETCH item unclosed header-list")
				return false
			}
			s.readChar()
		}
	}

	if s.peekChar() != ']' {
		s.Error = errors.New("imapparser: FETCH unclosed item section")
		return false
	}
	s.readChar()

	if s.peekChar() != '<' {
		return true
	}

	// Read partial range.
	s.readChar()

	v, err := s.readUint32()
	if err != nil {
		s.Error = errors.New("imapparser: FETCH invalid partial range start")
		return false
	}
	item.Partial.Start = v

	if s.peekChar() != '.' {
		s.Error = errors.New("imapparser: FETCH invalid partial range")
		return false
	}
	s.readChar()

	v, err = s.readUint32()
	if err != nil {
		s.Error = errors.New("imapparser: FETCH invalid partial range end")
		return false
	}
	item.Partial.Length = v

	if s.peekChar() != '>' {
		s.Error = errors.New("imapparser: FETCH invalid partial range close")
		return false
	}
	s.readChar()

	return true
}

func (s *Scanner) next(expect Token, allowEnd bool) bool {
	s.clear()

	s.consumeWhitespace()

	b := s.peekChar()

	switch b {
	case 0:
		s.readChar()
		if s.ioErr == io.EOF {
			s.Error = io.EOF
		} else if s.ioErr != nil && s.Error == nil {
			s.Error = s.ioErr
		}
		return false
	case '\r':
		s.readChar()
		b = s.peekChar()
		if b == '\n' {
			s.readChar()
			s.Token = TokenEnd
		} else {
			s.Error = fmt.Errorf(`imapparser: broken CRLF, "\r" followed by %q`, string(b))
		}
	case '\n':
		s.readChar()
		s.Token = TokenEnd
	default:
		switch expect {
		case TokenAtom:
			if s.readAtom() {
				s.Token = TokenAtom
			}
		case TokenString:
			// For strings, we limit the length of literals we accept.
			if s.readAstring() {
				s.Token = TokenString
			}
		case TokenNumber:
			var err error
			s.Number, err = s.readUint64()
			if err == nil {
				s.Token = TokenNumber
			}
		case TokenTag:
			if s.readTag() {
				s.Token = TokenTag
			}
		case TokenFlag:
			if s.readFlag() {
				s.Token = TokenFlag
			}
		case TokenSequences:
			if s.readSequences() {
				s.Token = TokenSequences
			}
		case TokenSearchKey:
			if b == '(' || b == ')' {
				s.readChar()
				s.Value = append(s.Value, b)
				s.Token = TokenSearchKey
			} else if isDigit(b) || b == '*' {
				if s.readSequences() {
					s.Token = TokenSearchKey
				}
			} else {
				if s.readAtom() {
					s.Token = TokenSearchKey
				}
			}
		case TokenFetchItem:
			if s.readFetchItem() {
				s.Token = TokenFetchItem
			}
		case TokenListStart:
			if s.readListStart() {
				s.Token = TokenListStart
			}
		case TokenListEnd:
			if ok, err := s.readListEnd(); err != nil {
				s.Error = err
				return false
			} else if ok {
				s.Token = TokenListEnd
			}
		case TokenDate:
			if s.readDate() {
				s.Token = TokenDate
			}
		case TokenListMailbox:
			if s.readListMailbox() {
				s.Token = TokenListMailbox
			}
		default:
			switch b {
			case '{':
				if s.readLiteral(0) {
					s.Token = TokenLiteral
				}
			case '"':
				if s.readQuotedString() {
					s.Token = TokenString
				}
			case '(':
				if s.readListStart() {
					s.Token = TokenListStart
				}
			case ')':
				if ok, err := s.readListEnd(); err != nil {
					s.Error = err
					return false
				} else if ok {
					s.Token = TokenListEnd
				}
			default:
				if s.readAtom() {
					s.Token = TokenAtom
				}
			}
		}
	}

	lastWasCRLF := s.Token == TokenEnd
	if s.Error == nil && expect != TokenUnknown && expect != s.Token {
		if !(allowEnd && s.Token == TokenEnd) {
			s.Token = TokenUnknown
		}
	}
	if s.Error != nil {
		s.Token = TokenUnknown
	}
	if s.Error != nil || s.Token == TokenUnknown {
		s.clear()
		s.lastWasCRLF = lastWasCRLF
		return false
	}
	return true
}
