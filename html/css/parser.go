package css

import (
	"math"
	"strconv"
)

// Parser parses CSS.
type Parser struct {
	s *Scanner
}

// NewParser creates a new CSS parser.
func NewParser(s *Scanner) *Parser {
	return &Parser{s: s}
}

// ParseDecl parses a CSS declaration.
// An HTML style="" attribute is a sequence of declarations.
//
// The passed Decl is cleared by reducing all the slices
// its elements reference to a length of zero.
// This allows the general reusing of allocations: any []byte
// in the slice under the initial cap(d) are sliced down to
// zero and then appended to.
func (p *Parser) ParseDecl(decl *Decl) bool {
	decl.clear()

	// CSS Syntax 5.4.4 "Consume a list of declarations", fraction
	p.next()
	switch p.s.Token {
	case EOF, Semicolon:
		return false
	case Ident:
		return p.parseDecl(decl)
	default:
		p.error("invalid token")
		for {
			p.next()
			if p.s.Token == EOF || p.s.Token == Semicolon {
				break
			}
		}
		return false
	}
}

func (p *Parser) next() {
	p.s.Next()
}

func (p *Parser) error(msg string) {
	p.s.ErrHandler(p.s.Line, p.s.Col, p.s.N, msg)
}

func (p *Parser) parseDecl(d *Decl) bool {
	// CSS Syntax 5.4.5 "Consume a declaration"
	d.Pos = Position{Line: p.s.Line, Col: p.s.Col}
	d.Property = append(d.Property, p.s.Value...)
	d.PropertyRaw = append(d.PropertyRaw, p.s.Literal...)
	p.next()
	if p.s.Token != Colon {
		p.error("bad declaration: expecting ':'")
		d.clear()
		return false
	}
	p.next()
	for p.s.Token != EOF && p.s.Token != Semicolon {
		if len(d.Values) == cap(d.Values) {
			d.Values = append(d.Values, Value{})
		} else {
			d.Values = d.Values[:len(d.Values)+1]
		}
		v := &d.Values[len(d.Values)-1]
		v.clear()
		v.Type, v.Data = p.valueType()
		v.Pos = Position{Line: p.s.Line, Col: p.s.Col}
		v.Raw = append(v.Raw, p.s.Literal...)
		v.Value = append(v.Value, p.s.Value...)
		p.next()
	}
	return true
}

func (p *Parser) valueType() (t ValueType, data uint64) {
	switch p.s.Token {
	case Ident:
		return ValueIdent, 0
	case Function:
		return ValueFunction, 0
	case Hash:
		// TODO: check flag to see if it's a ValueHashID
		return ValueHash, 0
	case String:
		return ValueString, 0
	case URL:
		return ValueURL, 0
	case Delim:
		return ValueDelim, 0
	case Number:
		if p.s.TypeFlag == TypeFlagInteger {
			v, err := strconv.ParseInt(string(p.s.Literal), 10, 64)
			if err != nil {
				panic("invalid integer: " + string(p.s.Literal))
			}
			return ValueInteger, uint64(v)
		}
		v, err := strconv.ParseFloat(string(p.s.Literal), 64)
		if err != nil {
			panic("invalid float: " + string(p.s.Literal))
		}
		return ValueNumber, math.Float64bits(v)
	case Percentage:
		v, err := strconv.ParseInt(string(p.s.Literal), 10, 64)
		if err != nil {
			panic("invalid percentage: " + string(p.s.Literal))
		}
		return ValuePercentage, uint64(v)
	case Dimension:
		return ValueDimension, 0 // TODO
	case UnicodeRange:
		return ValueUnicodeRange, 0 // TODO
	case IncludeMatch:
		return ValueIncludeMatch, 0
	case DashMatch:
		return ValueDashMatch, 0
	case PrefixMatch:
		return ValuePrefixMatch, 0
	case SuffixMatch:
		return ValueSuffixMatch, 0
	case SubstringMatch:
		return ValueSubstringMatch, 0
	case Comma:
		return ValueComma, 0
	}
	return ValueUnknown, 0
}

// Position is a line and column byte offset within a source document.
// It is used to report where a piece of parsed CSS was found.
type Position struct {
	Line int
	Col  int
}

// Decl is a CSS declaration.
type Decl struct {
	Pos           Position
	Property      []byte // escaped property name
	PropertyRaw   []byte // unescaped raw byte
	Values        []Value
	BangImportant bool
}

type Value struct {
	Pos   Position
	Type  ValueType
	Value []byte // escaped, processed value
	Raw   []byte // unescaped raw bytes underlying value
	Data  uint64 // encodes type-specific data, read via methods
}

type ValueType int

//go:generate stringer -type ValueType -linecomment

const (
	ValueUnknown        ValueType = iota // ValueUknown
	ValueIdent                           // ident
	ValueFunction                        // function
	ValueHash                            // hash
	ValueHashID                          // hash-id
	ValueString                          // string
	ValueURL                             // url
	ValueDelim                           // delim
	ValueNumber                          // num
	ValueInteger                         // int
	ValuePercentage                      // percent
	ValueDimension                       // dim
	ValueUnicodeRange                    // unocde-range
	ValueIncludeMatch                    // include-match
	ValueDashMatch                       // dash-match
	ValuePrefixMatch                     // prefix-match
	ValueSuffixMatch                     // suffix-match
	ValueSubstringMatch                  // substr-match
	ValueComma                           // comma
)

// TODO func (v *Value) Dimension() (value, uint []byte)
// TODO func (v *Value) URL() []byte

func (v *Value) Range() (start, end uint32) {
	if v.Type != ValueUnicodeRange {
		return 0, 0
	}
	return uint32(v.Data >> 32), uint32(v.Data)
}

func (v *Value) Number() float64 {
	if v.Type != ValueNumber {
		return 0
	}
	return math.Float64frombits(v.Data)
}

func (v *Value) Integer() int64 {
	if v.Type != ValueInteger {
		return 0
	}
	return int64(v.Data)
}

func (v *Value) clear() {
	v.Pos = Position{}
	v.Type = ValueUnknown
	if v.Value != nil {
		v.Value = v.Value[:0]
	}
	if v.Raw != nil {
		v.Raw = v.Raw[:0]
	}
}

func (d *Decl) clear() {
	d.Pos = Position{}
	if d.Property != nil {
		d.Property = d.Property[:0]
	}
	if d.PropertyRaw != nil {
		d.PropertyRaw = d.PropertyRaw[:0]
	}
	if d.Values != nil {
		for i := range d.Values {
			d.Values[i].clear()
		}
		d.Values = d.Values[:0]
	}
	d.BangImportant = false
}

/*
type Stylesheet struct {
	Rules []Rule
}

// Rule is either a CSS qualified rule or a CSS at-rule.
//
// Rules are only produced as part of a Stylesheet.
type Rule struct {
	// Either len(AtToken.Literal) > 0, len(Qualifiers) > 0, or neither.
	AtToken    Identifier
	Qualifiers []Component

	Block Component // Type == ComponentBlockBrace or ComponentNone
}


// Component is a block or a series of tokens (a "Component Value").
//
// It corresponds to several constructions the CSS Syntax spec
// which are distinguished by the Type field.
type Component struct {
	Pos  Position
	Type ComponentType

	// Either len(Token.Literal) > 0 or len(Values) > 0.
	Token  Identifier  // Type == ComponentValue
	Values []Component // Type != CompoonentValue
}

type ComponentType int

// Component types.
const (
	ComponentNone       ComponentType = iota
	ComponentValue                    // https://www.w3.org/TR/css-syntax-3/#component-value-diagram
	ComponentBlockBrace               // https://www.w3.org/TR/css-syntax-3/#%7B%7D-block-diagram
	ComponentBlockParen               // https://www.w3.org/TR/css-syntax-3/#%28%29-block-diagram
	ComponentBlockBrack               // https://www.w3.org/TR/css-syntax-3/#%5B%5D-block-diagram
	ComponentBlockFunc                // https://www.w3.org/TR/css-syntax-3/#function-block-diagram
)
*/
