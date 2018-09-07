package css

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type token struct {
	tok  Token
	sub  Subtype
	lit  string
	unit string
}

func (t token) String() string {
	if t.lit == "" && t.sub == SubtypeNone && t.unit == "" {
		return fmt.Sprintf("tok:%s", t.tok)
	}
	if t.sub == SubtypeNone && t.unit == "" {
		return fmt.Sprintf("{%s %q}", t.tok, t.lit)
	}
	return fmt.Sprintf("{%s %s %q %q}", t.tok, t.sub, t.lit, t.unit)
}

var scannerTests = []struct {
	input string
	want  []token
}{
	{
		input: `img  { foo: "Hello, 世界"  /* not a real rule */ }`,
		want: []token{
			{tok: Ident, lit: "img"},
			{tok: LeftBrace},
			{tok: Ident, lit: "foo"},
			{tok: Colon},
			{tok: String, lit: "Hello, 世界"},
			{tok: RightBrace},
			{tok: EOF},
		},
	},
	{
		input: `font-size: +2.3em;`,
		want: []token{
			{tok: Ident, lit: "font-size"},
			{tok: Colon},
			{tok: Dimension, sub: SubtypeNumber, lit: "+2.3", unit: "em"},
			{tok: Semicolon},
			{tok: EOF},
		},
	},
}

func TestScanner(t *testing.T) {
	for _, test := range scannerTests {
		t.Run(test.input, func(t *testing.T) {
			errh := func(line, col, n int, msg string) {
				t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
			}
			s := NewScanner(strings.NewReader(test.input), errh)
			var got []token
			for {
				s.Next()
				got = append(got, token{
					tok:  s.Token,
					lit:  string(s.Literal),
					sub:  s.Subtype,
					unit: string(s.Unit),
				})
				if s.Token == EOF {
					break
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("got  %v,\nwant %v", got, test.want)
			}
		})
	}
}
