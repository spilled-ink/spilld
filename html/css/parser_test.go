package css

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var parseDeclTests = []struct {
	name  string
	input string
	want  []Decl
	pos   bool
}{
	{
		input: `border: 1 solid #ababab; padding: 0; background: url("https://example.com/foo.svg")`,
		want: []Decl{
			decl("border", []Value{
				{Type: ValueInteger, Raw: b("1"), Number: 1},
				{Type: ValueIdent, Raw: b("solid"), Value: b("solid")},
				{Type: ValueHash, Raw: b("#ababab"), Value: b("ababab")},
			}),
			decl("padding", []Value{{
				Type: ValueInteger, Raw: b("0"),
			}}),
			decl("background", []Value{{
				Type:  ValueURL,
				Raw:   b(`url("https://example.com/foo.svg")`),
				Value: b("https://example.com/foo.svg"),
			}}),
		},
	},
	{
		input: `background:url("http://sketch.io/bar"), blue;`,
		want: []Decl{decl("background", []Value{
			{
				Type:  ValueURL,
				Raw:   b(`url("http://sketch.io/bar")`),
				Value: b("http://sketch.io/bar"),
			},
			{Type: ValueComma},
			{Type: ValueIdent, Raw: b("blue"), Value: b("blue")},
		})},
	},
	{
		input: `color: gray /* comment */; font-size: 5.67em;`,
		want: []Decl{
			decl("color", []Value{
				{Type: ValueIdent, Raw: b("gray"), Value: b("gray")},
			}),
			decl("font-size", []Value{
				{Type: ValueDimension, Raw: b("5.67em"), Number: 5.67, Value: b("em")},
			}),
		},
	},
	{
		name:  "value types",
		input: `list: a, "b", c, 7, 4.31e+9, 39%;`,
		want: []Decl{decl("list", []Value{
			{Type: ValueIdent, Raw: b("a"), Value: b("a")},
			{Type: ValueComma},
			{Type: ValueString, Raw: b(`"b"`), Value: b("b")},
			{Type: ValueComma},
			{Type: ValueIdent, Raw: b("c"), Value: b("c")},
			{Type: ValueComma},
			{Type: ValueInteger, Raw: b("7"), Number: 7},
			{Type: ValueComma},
			{Type: ValueNumber, Raw: b("4.31e+9"), Number: 4.31e+9},
			{Type: ValueComma},
			{Type: ValuePercentage, Raw: b("39%"), Number: 39},
		})},
	},
	{
		name:  "unicode ranges",
		input: `list: u+123???, u+5-f;`,
		want: []Decl{decl("list", []Value{
			{Type: ValueUnicodeRange, Raw: b("u+123???")},
			{Type: ValueComma},
			{Type: ValueUnicodeRange, Raw: b("u+5-f")},
		})},
	},
}

func b(s string) []byte { return []byte(s) }

func decl(name string, v []Value) Decl {
	return Decl{
		Property:    []byte(name),
		PropertyRaw: []byte(name),
		Values:      v,
	}
}

func TestParseDecl(t *testing.T) {
	for _, test := range parseDeclTests {
		name := test.name
		if name == "" {
			name = test.input
		}
		t.Run(name, func(t *testing.T) {
			errh := func(line, col, n int, msg string) {
				t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
			}
			p := NewParser(NewScanner(strings.NewReader(test.input), errh))

			var got []Decl
			for {
				var decl Decl
				if !p.ParseDecl(&decl) {
					break
				}
				if !test.pos {
					clearPos(&decl)
				}
				got = append(got, decl)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("\ngot:  %v\nwant: %v", sprintDecls(got), sprintDecls(test.want))
			}
		})
	}
}

func sprintDecls(decls []Decl) string {
	buf := new(bytes.Buffer)
	fprintDecls(buf, decls)
	return buf.String()
}

func fprintDecls(buf *bytes.Buffer, decls []Decl) {
	buf.WriteString("[")
	for i, decl := range decls {
		if i > 0 {
			buf.WriteByte(' ')
		}
		fprintDecl(buf, decl)
	}
	buf.WriteString("]")
}

func fprintVal(buf *bytes.Buffer, val Value) {
	if val.Pos != (Position{}) {
		fmt.Fprintf(buf, "%d:%d:", val.Pos.Line, val.Pos.Col)
	}
	fmt.Fprintf(buf, "%s:%q/%q:0x%f", val.Type, string(val.Raw), string(val.Value), val.Number)
}

func fprintDecl(buf *bytes.Buffer, decl Decl) {
	if decl.Pos == (Position{}) {
		fmt.Fprintf(buf, "{")
	} else {
		fmt.Fprintf(buf, "{%d%d:", decl.Pos.Line, decl.Pos.Col)
	}
	fmt.Fprintf(buf, "prop:%q/%q", string(decl.Property), string(decl.PropertyRaw))
	fmt.Fprintf(buf, " vals:[")
	for i, ident := range decl.Values {
		if i > 0 {
			buf.WriteString(", ")
		}
		fprintVal(buf, ident)
	}
	fmt.Fprintf(buf, "]}")
}

func clearPos(decl *Decl) {
	decl.Pos = Position{}
	for i := range decl.Values {
		decl.Values[i].Pos = Position{}
	}
}
