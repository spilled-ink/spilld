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
		input: `border: 1px solid #ababab; padding: 0; background: url("https://example.com/foo.svg")`,
		want: []Decl{
			decl("border", []Value{
				{Type: ValueDimension, Raw: b("1px")},
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
		input: `color: gray /* comment */; font-size: 5.67em;`,
		want: []Decl{
			decl("color", []Value{
				{Type: ValueIdent, Raw: b("gray"), Value: b("gray")},
			}),
			decl("font-size", []Value{
				{Type: ValueDimension, Raw: b("5.67em")},
			}),
		},
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
	fmt.Fprintf(buf, "%s:%q/%q:%x", val.Type, string(val.Raw), string(val.Value), val.Data)
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
