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
			{Property: ident("border"), Values: idents("1px", "solid", "#ababab")},
			{Property: ident("padding"), Values: idents("0")},
			{Property: ident("background"), Values: idents(`url("https://example.com/foo.svg")`)},
		},
	},
	{
		input: `color: gray /* comment */; font-size: 5.67em;`,
		want: []Decl{
			{Property: ident("color"), Values: idents("gray")},
			{Property: ident("font-size"), Values: idents("5.67em")},
		},
	},
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

func ident(s string) Identifier {
	return Identifier{Literal: []byte(s)}
}

func idents(strs ...string) (values []Identifier) {
	for _, s := range strs {
		values = append(values, ident(s))
	}
	return values
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

func fprintIdent(buf *bytes.Buffer, ident Identifier) {
	if ident.Pos == (Position{}) {
		fmt.Fprintf(buf, "%q", string(ident.Literal))
	} else {
		fmt.Fprintf(buf, "%d:%d:%q", ident.Pos.Line, ident.Pos.Col, string(ident.Literal))
	}
}

func fprintDecl(buf *bytes.Buffer, decl Decl) {
	fmt.Fprintf(buf, "{prop:")
	fprintIdent(buf, decl.Property)
	fmt.Fprintf(buf, " vals:[")
	for i, ident := range decl.Values {
		if i > 0 {
			buf.WriteByte(' ')
		}
		fprintIdent(buf, ident)
	}
	fmt.Fprintf(buf, "]}")
}

func clearPos(decl *Decl) {
	decl.Property.Pos = Position{}
	for i := range decl.Values {
		decl.Values[i].Pos = Position{}
	}
}
