package css

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type token struct {
	tok   Token
	sub   Subtype
	lit   string
	unit  string
	start uint32
	end   uint32
}

func (t token) String() string {
	if t.lit == "" && t.sub == SubtypeNone && t.unit == "" && t.start == 0 && t.end == 0 {
		return fmt.Sprintf("tok:%s", t.tok)
	}
	if t.sub == SubtypeNone && t.unit == "" && t.start == 0 && t.end == 0 {
		return fmt.Sprintf("{%s %q}", t.tok, t.lit)
	}
	if t.start == 0 && t.end == 0 {
		return fmt.Sprintf("{%s %s %q %q}", t.tok, t.sub, t.lit, t.unit)
	}

	return fmt.Sprintf("{%s %s %q %q 0x%x-0x%x}", t.tok, t.sub, t.lit, t.unit, t.start, t.end)
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
		input: `font-size: +2.3em; border: 0; fraction: .1;`,
		want: []token{
			{tok: Ident, lit: "font-size"},
			{tok: Colon},
			{tok: Dimension, sub: SubtypeNumber, lit: "+2.3", unit: "em"},
			{tok: Semicolon},
			{tok: Ident, lit: "border"},
			{tok: Colon},
			{tok: Number, sub: SubtypeInteger, lit: "0"},
			{tok: Semicolon},
			{tok: Ident, lit: "fraction"},
			{tok: Colon},
			{tok: Number, sub: SubtypeNumber, lit: ".1"},
			{tok: Semicolon},
			{tok: EOF},
		},
	},
	{
		input: `<!-- a || b |= c ~= @d *= e #f ua Ub -x \g -->`,
		want: []token{
			{tok: CDO},
			{tok: Ident, lit: "a"},
			{tok: Column},
			{tok: Ident, lit: "b"},
			{tok: DashMatch},
			{tok: Ident, lit: "c"},
			{tok: IncludeMatch},
			{tok: AtKeyword, lit: "d"},
			{tok: SubstringMatch},
			{tok: Ident, lit: "e"},
			{tok: Hash, lit: "f"},
			{tok: Ident, lit: "ua"},
			{tok: Ident, lit: "Ub"},
			{tok: Ident, lit: "-x"},
			{tok: Ident, lit: "g"},
			{tok: CDC},
			{tok: EOF},
		},
	},
	{
		input: `u+0102?? u+01-05 u+fa`,
		want: []token{
			{tok: UnicodeRange, start: 0x010200, end: 0x0102ff},
			{tok: UnicodeRange, start: 0x01, end: 0x05},
			{tok: UnicodeRange, start: 0xfa, end: 0xfa},
			{tok: EOF},
		},
	},
	{
		input: `"a\d\a" 5`,
		want: []token{
			{tok: String, lit: "a\r\n"},
			{tok: Number, sub: SubtypeInteger, lit: "5"},
			{tok: EOF},
		},
	},
}

func TestScanner(t *testing.T) {
	for _, test := range scannerTests {
		t.Run(test.input, func(t *testing.T) {
			errh := func(line, col, n int, msg string) {
				t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
				//panic("foo")
			}
			s := NewScanner(strings.NewReader(test.input), errh)
			var got []token
			for {
				s.Next()
				got = append(got, token{
					tok:   s.Token,
					lit:   string(s.Literal),
					sub:   s.Subtype,
					unit:  string(s.Unit),
					start: s.RangeStart,
					end:   s.RangeEnd,
				})
				println("next, token=", fmt.Sprintf("%v", got[len(got)-1]))
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
