package css

import (
	"io"
	"unicode/utf8"
)

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

type Subtype uint8

//go:generate stringer -type Subtype

// CSS Token subtypes.
const (
	SubtypeNone    Subtype = iota
	SubtypeID              // Hash
	SubtypeNumber          // Number
	SubtypeInteger         // Number
)

type Scanner struct {
	Source     *SourceReader
	ErrHandler func(line, col, n int, msg string)

	Token   Token
	Subtype Subtype
	Literal []byte
	Unit    []byte // valid when Token == Dimension

	nextIsBangDelim bool // extra lookahead
}

func NewScanner(src io.Reader, errHandler func(line, col, n int, msg string)) *Scanner {
	s := &Scanner{
		Source:     NewSourceReader(src, 0),
		ErrHandler: errHandler,
		Literal:    make([]byte, 0, 128),
	}

	// CSS Syntax 3.3 replace NULL with Unicode replacement character
	s.Source.SetReplaceNULL(true)

	return s
}

func (s *Scanner) error(msg string) {
	line, col, n := s.Source.Pos()
	s.ErrHandler(line, col, n, msg)
}

// Next reads the next token to advance the scanner.
func (s *Scanner) Next() {
	s.Token = Unknown
	s.Subtype = SubtypeNone
	s.Literal = s.Literal[:0]
	s.Unit = nil

	// CSS Syntax 4.3.1 consume as much whitespace as possible
redo:
	c := s.Source.GetRune()
	for isWhitespace(c) {
		c = s.Source.GetRune()
	}

	switch c {
	case -1:
		if err := s.Source.Error(); err != nil {
			if err != io.EOF {
				s.error(err.Error())
			}
		}
		s.Token = EOF

	case '"':
		s.Token = String
		s.string('"')

	case '#':
		c = s.Source.GetRune()
		hasName := isNameCodePoint(c) || isEscape(c, s.Source.PeekRune())
		s.Source.UngetRune()

		if hasName {
			// "If the next input code point is a name code point
			// or the next two input code points are a valid escape"
			s.Token = Hash
			s.name()
			// TODO: if s.Literal starts as an identifier, set type flag to "id"
			//s.Subtype = ID
		} else {
			// "Otherwise, return a <delim-token> with its value
			// set to the current input code point."
			s.Token = Delim
			s.Literal = append(s.Literal, '#')
		}

	case '$':
		if s.Source.PeekRune() == '=' {
			s.Source.GetRune()
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
		if s.Source.PeekRune() == '=' {
			s.Source.GetRune()
			s.Token = SubstringMatch
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '*')
		}

	case '+':
		c = s.Source.GetRune()
		if isNumber('+', c, s.Source.PeekRune()) {
			s.Source.UngetRune()
			s.numeric('+')
		} else {
			s.Source.UngetRune()
			s.Token = Delim
			s.Literal = append(s.Literal, '+')
		}

	case ',':
		s.Token = Comma

	case '-':
		// TODO

	case '.':
		// TODO

	case '/':
		if s.Source.PeekRune() == '*' {
			s.Source.GetRune()
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
		var runes [3]rune
		err := s.Source.PeekRunes(runes[:])
		if err == nil && runes[0] == '!' && runes[1] == '-' && runes[2] == '-' {
			s.Source.GetRune()
			s.Source.GetRune()
			s.Source.GetRune()
			s.Token = CDO
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '<')
		}

	case '@':
		// TODO

	case '[':
		s.Token = LeftBrack

	case '\\':
		// TODO

	case ']':
		s.Token = RightBrack

	case '^':
		if s.Source.PeekRune() == '=' {
			s.Source.GetRune()
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
		// TODO

	case '|':
		// TODO

	case '~':
		if s.Source.PeekRune() == '=' {
			s.Source.GetRune()
			s.Token = IncludeMatch
		} else {
			s.Token = Delim
			s.Literal = append(s.Literal, '~')
		}

	default:
		if isDigit(c) {
			// TODO
		} else if isNameStartCodePoint(c) {
			// CSS Syntax 4.3.3. Consume an ident-like token
			s.Source.UngetRune()
			s.name()

			if len(s.Literal) == 3 && string(s.Literal) == "url" && s.Source.PeekRune() == '(' {
				// "If the returned string’s value is an ASCII
				// case-insensitive match for "url", and the next
				// input code point is U+0028 LEFT PARENTHESIS ((),
				// consume it. Consume a url token, and return it."
				s.Source.GetRune()
				s.url()
			} else if s.Source.PeekRune() == '(' {
				// "Otherwise, if the next input code point is
				// U+0028 LEFT PARENTHESIS ((), consume it.
				// Create a <function-token> with its value set
				// to the returned string and return it.""
				s.Source.GetRune()
				s.Token = Function
			} else {
				s.Token = Ident
			}
		} else {
			s.Token = Delim
			s.Literal = appendRune(s.Literal, c)
		}
	}
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

func (s *Scanner) numeric(c rune) {
	// CSS Syntax 4.3.2 "Consume a numeric token"
	s.number(c)

	var p [3]rune
	s.Source.PeekRunes(p[:])
	if isIdent(p[0], p[1], p[2]) {
		s.Token = Dimension
		lit := s.Literal
		s.Literal = s.Literal[len(s.Literal):]
		s.name()
		s.Unit = s.Literal
		s.Literal = lit
	} else if p[0] == '%' {
		s.Token = Percentage
		s.Source.GetRune()
	} else {
		s.Token = Number
	}
}

func (s *Scanner) number(c rune) {
	// CSS Syntax 4.3.12 "Consume a number"
	s.Token = Number
	s.Subtype = SubtypeInteger

	if c == '+' || c == '-' {
		s.Literal = appendRune(s.Literal, c)
		c = s.Source.GetRune()
	}
	for isDigit(c) {
		s.Literal = appendRune(s.Literal, c)
		c = s.Source.GetRune()
	}
	if c == '.' && isDigit(s.Source.PeekRune()) {
		s.Subtype = SubtypeNumber
		s.Literal = appendRune(s.Literal, '.')
		s.Literal = appendRune(s.Literal, s.Source.GetRune())
		c = s.Source.GetRune()
		for isDigit(c) {
			s.Literal = appendRune(s.Literal, c)
			c = s.Source.GetRune()
		}
	}
	if c == 'e' || c == 'E' {
		var p [2]rune
		s.Source.PeekRunes(p[:])

		if isDigit(p[0]) || ((p[0] == '-' || p[0] == '+') && isDigit(p[1])) {
			s.Subtype = SubtypeNumber
			s.Literal = appendRune(s.Literal, p[0])
			s.Literal = appendRune(s.Literal, p[1])
			s.Source.GetRune()
			s.Source.GetRune()

			for {
				if c := s.Source.PeekRune(); isDigit(c) {
					s.Literal = appendRune(s.Literal, c)
					s.Source.GetRune()
				}
			}
		} else {
			s.Source.UngetRune()
		}
	}
}

func (s *Scanner) url() {
	// CSS Syntax 4.3.5 "Consume a url token"
	s.Token = URL
	s.Literal = s.Literal[:0]

	c := s.Source.GetRune()
	for isWhitespace(c) {
		c = s.Source.GetRune()
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
		c := s.Source.GetRune()
		for isWhitespace(c) {
			c = s.Source.GetRune()
		}

		if c == ')' {
			return
		} else {
			s.Source.UngetRune()
			s.badURLRemnants()
			return
		}
	}

	for {
		for isWhitespace(c) {
			c = s.Source.GetRune()
		}

		switch c {
		case ')', -1:
			return
		case '"', '\'', '(':
			s.badURLRemnants()
			return
		case '\\':
			if isEscape(c, s.Source.PeekRune()) {
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

		c = s.Source.GetRune()
	}
}

func (s *Scanner) badURLRemnants() {
	s.Token = BadURL
	s.Literal = s.Literal[:0]
	// CSS Syntax 4.3.14 "Consume the remnants of a bad url"
	for {
		c := s.Source.GetRune()
		switch {
		case c == ')' || c == -1:
			return
		case isEscape(c, s.Source.PeekRune()):
			s.Source.UngetRune()
			s.escape()
		}
	}
}

func (s *Scanner) name() {
	for {
		c := s.Source.GetRune()
		switch {
		case isNameCodePoint(c):
			s.Literal = appendRune(s.Literal, c)
		case c == '\\':
			if s.Source.PeekRune() != '\n' {
				s.Literal = appendRune(s.Literal, s.escape())
				continue
			}
			fallthrough
		default:
			s.Source.UngetRune()
			return
		}
	}
}

func (s *Scanner) string(quote rune) {
	s.Literal = s.Literal[:0]

	for {
		c := s.Source.GetRune()
		if c == quote {
			return
		}
		switch c {
		case -1:
			s.error("unterminated string")
			return
		case '\n':
			s.Token = BadString
			s.error("newline in string")
			return
		case '\\':
			c = s.Source.GetRune()
			if c == -1 {
				continue
			}
			if c != '\n' {
				s.Source.UngetRune()
				c = s.escape()
			}
		}

		s.Literal = appendRune(s.Literal, c)
	}
}

func (s *Scanner) skipComment() {
	for c := s.Source.GetRune(); c >= 0; c = s.Source.GetRune() {
		for c == '*' {
			c = s.Source.GetRune()
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
	c := s.Source.GetRune()

	// "EOF code point: return replacement character"
	if c == -1 {
		return '\uFFFD'
	}

	// "hex digit"
	if d0, isHex := asHex(c); isHex {
		d := uint32(d0)

		// "Consume as many hex digits as possible, but no more than 5."
		for i := 0; i < 5; i++ {
			c = s.Source.GetRune()
			d0, isHex := asHex(c)
			if isHex {
				d <<= 8
				d |= uint32(d0)
			} else {
				break
			}
		}
		// "If the next input code point is whitespace, consume it as well."
		if !isWhitespace(c) {
			s.Source.UngetRune()
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

func asHex(c rune) (uint8, bool) {
	switch {
	case '0' <= c && c <= '9':
		return 0x0 + uint8('0'-c), true
	case 'a' <= c && c <= 'f':
		return 0xa + uint8('a'-c), true
	case 'A' <= c && c <= 'F':
		return 0xa + uint8('A'-c), true
	default:
		return 0, false
	}
}
