package css

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
	d.Property.Pos = Position{Line: p.s.Line, Col: p.s.Col}
	d.Property.Literal = append(d.Property.Literal, p.s.Literal...)
	p.next()
	if p.s.Token != Colon {
		p.error("bad declaration: expecting ':'")
		d.clear()
		return false
	}
	p.next()
	for p.s.Token != EOF && p.s.Token != Semicolon {
		if len(d.Values) == cap(d.Values) {
			d.Values = append(d.Values, Identifier{})
		} else {
			d.Values = d.Values[:len(d.Values)+1]
		}
		ident := &d.Values[len(d.Values)-1]
		ident.clear()
		d.Property.Pos = Position{Line: p.s.Line, Col: p.s.Col}
		ident.Literal = append(ident.Literal, p.s.Literal...)
		p.next()
	}
	return true
}

type Position struct {
	Line int
	Col  int
}

type Identifier struct {
	Pos     Position
	Literal []byte
}

// Decl is a CSS declaration.
type Decl struct {
	Property      Identifier
	Values        []Identifier
	BangImportant bool
}

func (i *Identifier) clear() {
	i.Pos = Position{}
	if i.Literal != nil {
		i.Literal = i.Literal[:0]
	}
}

func (d *Decl) clear() {
	d.Property.clear()
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
