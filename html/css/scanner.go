package css

import (
	"io"
	"unicode/utf8"
)

// Token is a CSS Token type.
type Token uint8

//go:generate stringer -type Token

// CSS Tokens.
// Defined in section 4 of https://www.w3.org/TR/css-syntax-3/#tokenization.
const (
	Unknown Token = iota
	EOF
	Ident
	Function
	AtKeyword
	Hash
	String
	BadString
	URL
	BadURL
	Delim
	Number
	Percentage
	Dimension
	UnicodeRange
	IncludeMatch
	DashMatch
	PrefixMatch
	SuffixMatch
	SubstringMatch
	Column
	CDO        // <!--
	CDC        // -->
	Colon      // :
	Semicolon  // ;
	Comma      // ,
	LeftBrack  // [
	RightBrack // ]
	LeftParen  // (
	RightParen // )
	LeftBrace  // {
	RightBrace // }
)

// TypeFlag is a CSS Token type flag.
type TypeFlag uint8

//go:generate stringer -type TypeFlag

// CSS Token subtypes.
const (
	TypeFlagNone    TypeFlag = iota
	TypeFlagID               // Hash
	TypeFlagNumber           // Number
	TypeFlagInteger          // Number
)

// Scanner reads CSS tokens from a byte stream.
type Scanner struct {
	// ErrHandler is called when the tokenizer encounters a CSS parse error
	// or the underlying io.Reader reports a non-EOF error.
	ErrHandler func(line, col, n int, msg string)

	// Token results
	Token      Token
	TypeFlag   TypeFlag // for Hash, Numberr
	Literal    []byte   // backing array reused when Next is called
	Unit       []byte   // for Dimension, backing array reused when Next is called
	RangeStart uint32   // for UnicodeRange
	RangeEnd   uint32   // for UnicodeRange
	Line       int      // line number at token beginning
	Col        int      // column offset in bytes at token beginning

	source *_SourceReader
}

func NewScanner(src io.Reader, errHandler func(line, col, n int, msg string)) *Scanner {
	s := &Scanner{
		source:     _NewSourceReader(src, 0),
		ErrHandler: errHandler,
		Literal:    make([]byte, 0, 128),
	}

	// CSS Syntax 3.3 replace NULL with Unicode replacement character
	s.source.SetReplaceNULL(true)

	return s
}

func (s *Scanner) error(msg string) {
	line, col, n := s.source.Pos()
	s.ErrHandler(line, col, n, msg)
}

// Next reads the next token to advance the scanner.
func (s *Scanner) Next() {
	s.Token = Unknown
	s.TypeFlag = TypeFlagNone
	s.Literal = s.Literal[:0]
	s.Unit = nil
	s.RangeStart = 0
	s.RangeEnd = 0

	// CSS Syntax 4.3.1 consume as much whitespace as possible
redo:
	s.Line, s.Col, _ = s.source.Pos()
	c := s.source.GetRune()
	for isWhitespace(c) {
		s.Line, s.Col, _ = s.source.Pos()
		c = s.source.GetRune()
	}

	switch c {
	case -1:
		if err := s.source.Error(); err != nil {
			if err != io.EOF {
				s.error(err.Error())
			}
		}
		s.Token = EOF

	case '"':
		s.Token = String
		s.string('"')

	case '#':
		s.hash()

	case '$':
		if s.source.PeekRune() == '=' {
			s.source.GetRune()
			s.Token = SuffixMatch
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '$')
		}

	case '\'':
		s.Token = String
		s.string('\'')

	case '(':
		s.Token = LeftParen

	case ')':
		s.Token = RightParen

	case '*':
		if s.source.PeekRune() == '=' {
			s.source.GetRune()
			s.Token = SubstringMatch
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '*')
		}

	case '+':
		c = s.source.GetRune()
		if isNumber('+', c, s.source.PeekRune()) {
			s.source.UngetRune()
			s.numeric('+')
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '+')
		}

	case ',':
		s.Token = Comma

	case '-':
		var p [3]rune
		s.source.PeekRunes(p[:])
		if isDigit(p[0]) {
			s.numeric(c)
		} else if isIdent(p[0], p[1], p[2]) {
			s.source.UngetRune()
			s.identLike()
		} else if p[0] == '-' && p[1] == '>' {
			s.source.GetRune()
			s.source.GetRune()
			s.Token = CDC
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '-')
		}

	case '.':
		if isDigit(s.source.PeekRune()) {
			s.numeric(c)
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '.')
		}

	case '/':
		if s.source.PeekRune() == '*' {
			s.source.GetRune()
			s.skipComment()
			goto redo
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '/')
		}

	case ':':
		s.Token = Colon

	case ';':
		s.Token = Semicolon

	case '<':
		var p [3]rune
		s.source.PeekRunes(p[:])
		if p[0] == '!' && p[1] == '-' && p[2] == '-' {
			s.source.GetRune()
			s.source.GetRune()
			s.source.GetRune()
			s.Token = CDO
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '<')
		}

	case '@':
		var p [3]rune
		s.source.PeekRunes(p[:])
		if isIdent(p[0], p[1], p[2]) {
			s.name()
			s.Token = AtKeyword
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '@')
		}

	case '[':
		s.Token = LeftBrack

	case '\\':
		if isEscape(c, s.source.PeekRune()) {
			s.source.UngetRune()
			s.identLike()
		} else {
			s.error("invalid escape character")
			s.Token = Delim
			s.Literal = append(s.Literal, '\\')
		}

	case ']':
		s.Token = RightBrack

	case '^':
		if s.source.PeekRune() == '=' {
			s.source.GetRune()
			s.Token = PrefixMatch
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '^')
		}

	case '{':
		s.Token = LeftBrace

	case '}':
		s.Token = RightBrace

	case 'U', 'u':
		var p [2]rune
		s.source.PeekRunes(p[:])
		if p[0] == '+' && (isHex(p[1]) || p[1] == '?') {
			s.source.GetRune() // consume '+'
			s.unicodeRange()
		} else {
			s.source.UngetRune()
			s.identLike()
		}

	case '|':
		switch s.source.PeekRune() {
		case '=':
			s.source.GetRune()
			s.Token = DashMatch
		case '|':
			s.source.GetRune()
			s.Token = Column
		default:
			s.Token = Delim
			s.Literal = append(s.Literal, '|')
		}

	case '~':
		if s.source.PeekRune() == '=' {
			s.source.GetRune()
			s.Token = IncludeMatch
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '~')
		}

	default:
		if isDigit(c) {
			s.numeric(c)
		} else if isNameStartCodePoint(c) {
			s.source.UngetRune()
			s.identLike()
		} else {
			s.Token = Delim
			s.Literal = appendRune(s.Literal, c)
		}
	}
}

func isHex(c rune) bool {
	return isDigit(c) || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

func isNameCodePoint(c rune) bool {
	// CSS Syntax 4.2 "A name-start code point, A digit, or U+002D HYPHEN-MINUS (-)."
	return isNameStartCodePoint(c) || isDigit(c) || c == '-'
}

func isNameStartCodePoint(c rune) bool {
	// CSS Syntax 4.2 "A letter, a non-ASCII code point, or U+005F LOW LINE (_).""
	return isLetter(c) || c >= utf8.RuneSelf || c == '_'
}

func isNumber(c0, c1, c2 rune) bool {
	// CSS Syntax 4.3.10 "Check if three code points would start a number"
	switch {
	case c0 == '+', c0 == '-':
		if isDigit(c1) {
			return true
		} else if c1 == '.' && isDigit(c2) {
			return true
		}
		return false
	case c0 == '.':
		return isDigit(c1)
	case isDigit(c0):
		return true
	}
	return false
}

func isLetter(c rune) bool {
	// CSS Syntax 4.2 "An uppercase letter or a lowercase letter."
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isDigit(c rune) bool {
	// CSS Syntax 4.2 "A code point between U+0030 DIGIT ZERO (0) and U+0039 DIGIT NINE (9)."
	return '0' <= c && c <= '9'
}

func isEscape(c0, c1 rune) bool {
	// CSS Syntax 4.3.8 "Check if two code points are a valid escape"
	return c0 == '\\' && c1 != '\n'
}

func isNonPrintable(c rune) bool {
	// CSS Syntax 4.2
	return (0 <= c && c <= '\u0008') || c == '\u000b' || ('\u000e' <= c && c <= '\u001f') || c == '\u007f'
}

func isIdent(c0, c1, c2 rune) bool {
	if c0 == '-' {
		return isNameStartCodePoint(c1) || isEscape(c1, c2)
	} else if isNameStartCodePoint(c0) {
		return true
	}
	return isEscape(c0, c1)
}

func (s *Scanner) hash() {
	var p [2]rune
	s.source.PeekRunes(p[:])
	hasName := isNameCodePoint(p[0]) || isEscape(p[0], p[1])

	if hasName {
		// "If the next input code point is a name code point
		// or the next two input code points are a valid escape"
		s.Token = Hash
		s.name()
		// TODO: if s.Literal starts as an identifier, set type flag to "id"
		//s.TypeFlag = ID
	} else {
		// "Otherwise, return a <delim-token> with its value
		// set to the current input code point."
		s.Token = Delim
		s.Literal = append(s.Literal, '#')
	}
}

func (s *Scanner) unicodeRange() {
	// CSS Syntax 4.3.6 "Consume a unicode-range token"

	// "Consume as many hex digits as possible, but no more than 6."
	var d uint32
	d, i := s.hex(6)
	numQM := 0
	for i < 6 {
		if s.source.PeekRune() == '?' {
			s.source.GetRune()
			numQM++
		} else {
			break
		}
		i++
	}

	if numQM > 0 {
		rangeStart := d
		rangeEnd := d
		for i := numQM; i > 0; i-- {
			rangeStart <<= 4
			rangeEnd <<= 4
			rangeEnd |= uint32(0xf)
		}
		s.RangeStart = rangeStart
		s.RangeEnd = rangeEnd
		s.Token = UnicodeRange
		return
	}

	s.RangeStart = d

	var p [2]rune
	s.source.PeekRunes(p[:])
	if p[0] == '-' && isHex(p[1]) {
		s.source.GetRune()
		s.RangeEnd, _ = s.hex(6)
		s.Token = UnicodeRange
		return
	}

	s.RangeEnd = s.RangeStart
	s.Token = UnicodeRange
	return
}

func (s *Scanner) identLike() {
	// CSS Syntax 4.3.3. Consume an ident-like token
	s.name()

	if len(s.Literal) == 3 && string(s.Literal) == "url" && s.source.PeekRune() == '(' {
		// "If the returned string’s value is an ASCII
		// case-insensitive match for "url", and the next
		// input code point is U+0028 LEFT PARENTHESIS ((),
		// consume it. Consume a url token, and return it."
		s.source.GetRune()
		s.url()
	} else if s.source.PeekRune() == '(' {
		// "Otherwise, if the next input code point is
		// U+0028 LEFT PARENTHESIS ((), consume it.
		// Create a <function-token> with its value set
		// to the returned string and return it.""
		s.source.GetRune()
		s.Token = Function
	} else {
		s.Token = Ident
	}
}

func (s *Scanner) numeric(c rune) {
	// CSS Syntax 4.3.2 "Consume a numeric token"
	s.number(c)

	var p [3]rune
	s.source.PeekRunes(p[:])
	if isIdent(p[0], p[1], p[2]) {
		s.Token = Dimension
		lit := s.Literal
		s.Literal = s.Literal[len(s.Literal):]
		s.name()
		s.Unit = s.Literal
		s.Literal = lit
	} else if p[0] == '%' {
		s.Token = Percentage
		s.source.GetRune()
	} else {
		s.Token = Number
	}
}

func (s *Scanner) number(c rune) {
	// CSS Syntax 4.3.12 "Consume a number"
	s.Token = Number
	s.TypeFlag = TypeFlagInteger

	if c == '+' || c == '-' {
		s.Literal = appendRune(s.Literal, c)
		c = s.source.GetRune()
	}
	for isDigit(c) {
		s.Literal = appendRune(s.Literal, c)
		c = s.source.GetRune()
	}
	if c == '.' && isDigit(s.source.PeekRune()) {
		s.TypeFlag = TypeFlagNumber
		s.Literal = appendRune(s.Literal, '.')
		s.Literal = appendRune(s.Literal, s.source.GetRune())
		c = s.source.GetRune()
		for isDigit(c) {
			s.Literal = appendRune(s.Literal, c)
			c = s.source.GetRune()
		}
	}
	if c == 'e' || c == 'E' {
		var p [2]rune
		s.source.PeekRunes(p[:])

		if isDigit(p[0]) || ((p[0] == '-' || p[0] == '+') && isDigit(p[1])) {
			s.TypeFlag = TypeFlagNumber
			s.Literal = appendRune(s.Literal, c)
			s.Literal = appendRune(s.Literal, p[0])
			s.Literal = appendRune(s.Literal, p[1])
			s.source.GetRune()
			s.source.GetRune()

			c = s.source.GetRune()
			for isDigit(c) {
				s.Literal = appendRune(s.Literal, c)
				c = s.source.GetRune()
			}
		}
	}
	if c != -1 {
		s.source.UngetRune()
	}
}

func (s *Scanner) url() {
	// CSS Syntax 4.3.5 "Consume a url token"
	s.Token = URL
	s.Literal = s.Literal[:0]

	c := s.source.GetRune()
	for isWhitespace(c) {
		c = s.source.GetRune()
	}

	if c == -1 {
		return
	}

	if c == '"' || c == '\'' {
		s.string(c) // clobbers s.Token
		if s.Token == BadString {
			s.badURLRemnants()
			return
		}

		s.Token = URL
		c := s.source.GetRune()
		for isWhitespace(c) {
			c = s.source.GetRune()
		}

		if c == ')' {
			return
		} else {
			s.source.UngetRune()
			s.badURLRemnants()
			return
		}
	}

	for {
		for isWhitespace(c) {
			c = s.source.GetRune()
		}

		switch c {
		case ')', -1:
			return
		case '"', '\'', '(':
			s.badURLRemnants()
			return
		case '\\':
			if isEscape(c, s.source.PeekRune()) {
				s.Literal = appendRune(s.Literal, s.escape())
			} else {
				// parse error
				s.badURLRemnants()
				return
			}
		default:
			if isNonPrintable(c) {
				s.badURLRemnants()
				return
			}
			s.Literal = appendRune(s.Literal, c)
		}

		c = s.source.GetRune()
	}
}

func (s *Scanner) badURLRemnants() {
	s.Token = BadURL
	s.Literal = s.Literal[:0]
	// CSS Syntax 4.3.14 "Consume the remnants of a bad url"
	for {
		c := s.source.GetRune()
		switch {
		case c == ')' || c == -1:
			return
		case isEscape(c, s.source.PeekRune()):
			s.source.UngetRune()
			s.escape()
		}
	}
}

func (s *Scanner) name() {
	for {
		c := s.source.GetRune()
		switch {
		case isNameCodePoint(c):
			s.Literal = appendRune(s.Literal, c)
		case c == '\\':
			if s.source.PeekRune() != '\n' {
				s.Literal = appendRune(s.Literal, s.escape())
				continue
			}
			fallthrough
		case c == -1:
			return
		default:
			s.source.UngetRune()
			return
		}
	}
}

func (s *Scanner) string(quote rune) {
	s.Literal = s.Literal[:0]

	for {
		c := s.source.GetRune()
		if c == quote {
			return
		}
		switch c {
		case -1:
			s.Literal = s.Literal[:0]
			s.Token = BadString
			s.error("unterminated string")
			return
		case '\n':
			s.Literal = s.Literal[:0]
			s.Token = BadString
			s.error("newline in string")
			return
		case '\\':
			c = s.source.GetRune()
			if c == -1 {
				continue
			}
			if c != '\n' {
				s.source.UngetRune()
				c = s.escape()
			}
		}

		s.Literal = appendRune(s.Literal, c)
	}
}

func (s *Scanner) skipComment() {
	for c := s.source.GetRune(); c >= 0; c = s.source.GetRune() {
		for c == '*' {
			c = s.source.GetRune()
			if c == '/' {
				return
			}
		}
	}
	s.error("unterminated comment")
}

func appendRune(slice []byte, c rune) []byte {
	var b [4]byte
	return append(slice, b[:utf8.EncodeRune(b[:], c)]...)
}

func (s *Scanner) escape() rune {
	// CSS Syntax 4.3.7 Consume an escaped code point
	c := s.source.GetRune()

	// "EOF code point: return replacement character"
	if c == -1 {
		return '\uFFFD'
	}

	// "hex digit"
	if isHex(c) {
		// "Consume as many hex digits as possible, but no more than 5."
		s.source.UngetRune()
		d, _ := s.hex(6)

		// "If the next input code point is whitespace, consume it as well."
		if isWhitespace(s.source.PeekRune()) {
			s.source.GetRune()
		}

		switch {
		case 0xD800 <= d && d <= 0xDFFF:
			// Surrogate code point, replace with replacement.
			fallthrough
		case d >= uint32(utf8.MaxRune):
			c = '\uFFFD'
		default:
			c = rune(d)
		}
	}

	// "anything else: Return the current input code point."
	return c
}

func isWhitespace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func (s *Scanner) hex(maxCount int) (d uint32, count int) {
	for count = 0; count < maxCount; count++ {
		d0, isHex := asHex(s.source.PeekRune())
		if isHex {
			s.source.GetRune()
			d <<= 4
			d |= uint32(d0)
		} else {
			break
		}
	}
	return d, count
}

func asHex(c rune) (uint8, bool) {
	switch {
	case '0' <= c && c <= '9':
		return 0x0 + uint8(c-'0'), true
	case 'a' <= c && c <= 'f':
		return 0xa + uint8(c-'a'), true
	case 'A' <= c && c <= 'F':
		return 0xa + uint8(c-'A'), true
	default:
		return 0, false
	}
}
