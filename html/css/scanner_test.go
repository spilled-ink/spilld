package css

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type pos struct {
	line int
	col  int
}

func (p pos) String() string {
	return fmt.Sprintf("%d:%d", p.line, p.col)
}

type token struct {
	pos   pos
	tok   Token
	sub   TypeFlag
	lit   string
	unit  string
	start uint32
	end   uint32
}

func (t token) String() string {
	if t.lit == "" && t.sub == TypeFlagNone && t.unit == "" && t.start == 0 && t.end == 0 {
		return fmt.Sprintf("%s:tok:%s", t.pos, t.tok)
	}
	if t.sub == TypeFlagNone && t.unit == "" && t.start == 0 && t.end == 0 {
		return fmt.Sprintf("{%s:%s %q}", t.pos, t.tok, t.lit)
	}
	if t.start == 0 && t.end == 0 {
		return fmt.Sprintf("{%s:%s %s %q %q}", t.pos, t.tok, t.sub, t.lit, t.unit)
	}

	return fmt.Sprintf("{%s:%s %s %q %q 0x%x-0x%x}", t.pos, t.tok, t.sub, t.lit, t.unit, t.start, t.end)
}

var scannerTests = []struct {
	name  string
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
			{tok: Dimension, sub: TypeFlagNumber, lit: "+2.3", unit: "em"},
			{tok: Semicolon},
			{tok: Ident, lit: "border"},
			{tok: Colon},
			{tok: Number, sub: TypeFlagInteger, lit: "0"},
			{tok: Semicolon},
			{tok: Ident, lit: "fraction"},
			{tok: Colon},
			{tok: Number, sub: TypeFlagNumber, lit: ".1"},
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
		name:  "unicode range tests",
		input: `u+0102?? u+01-05 u+fa`,
		want: []token{
			{tok: UnicodeRange, start: 0x010200, end: 0x0102ff},
			{tok: UnicodeRange, start: 0x01, end: 0x05},
			{tok: UnicodeRange, start: 0xfa, end: 0xfa},
			{tok: EOF},
		},
	},
	{
		name:  "escape tests",
		input: `"a\d\a" 5`,
		want: []token{
			{tok: String, lit: "a\r\n"},
			{tok: Number, sub: TypeFlagInteger, lit: "5"},
			{tok: EOF},
		},
	},
	{
		name:  "infinite ident loop (from go-fuzz)",
		input: "\x80",
		want: []token{
			{tok: Ident, lit: "\uFFFD"},
			{tok: EOF},
		},
	},
	{
		name:  "infinite + loop (from go-fuzz)",
		input: "+",
		want: []token{
			{tok: Delim, lit: "+"},
			{tok: EOF},
		},
	},
	{
		name:  "url tests",
		input: `background:url("https://example.com/foo");`,
		want: []token{
			{tok: Ident, lit: "background"},
			{tok: Colon},
			{tok: URL, lit: "https://example.com/foo"},
			{tok: Semicolon},
			{tok: EOF},
		},
	},
}

func TestScanner(t *testing.T) {
	for _, test := range scannerTests {
		name := test.name
		if name == "" {
			name = test.input
		}
		t.Run(name, func(t *testing.T) {
			errh := func(line, col, n int, msg string) {
				t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
			}
			s := NewScanner(strings.NewReader(test.input), errh)
			var got []token
			for {
				s.Next()
				got = append(got, token{
					//pos:   pos{s.Line, s.Col},
					tok:   s.Token,
					lit:   string(s.Literal),
					sub:   s.TypeFlag,
					unit:  string(s.Unit),
					start: s.RangeStart,
					end:   s.RangeEnd,
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

func TestScannerFiles(t *testing.T) {
	files, err := filepath.Glob("testdata/*.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			f, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			errh := func(line, col, n int, msg string) {
				t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
			}
			s := NewScanner(f, errh)
			for {
				s.Next()
				tok := token{
					pos:   pos{s.Line, s.Col},
					tok:   s.Token,
					lit:   string(s.Literal),
					sub:   s.TypeFlag,
					unit:  string(s.Unit),
					start: s.RangeStart,
					end:   s.RangeEnd,
				}
				t.Log(tok)

				if s.Token == EOF {
					break
				}
			}
		})
	}
}
