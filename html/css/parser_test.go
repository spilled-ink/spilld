package css

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var parseAndFormatDeclTests = []struct {
	name string
	text string
	decl []Decl
}{
	{
		text: `background: url("http://sketch.io/bar"), blue;`,
		decl: []Decl{decl("background", []Value{
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
		name: "url_encoding",
		text: `background: url("https://example.com/\"a\""), blue;`,
		decl: []Decl{{
			Property:    b("background"),
			PropertyRaw: b("background"),
			Values: []Value{
				{
					Type:  ValueURL,
					Raw:   b(`url("https://example.com/\"a\"")`),
					Value: b("https://example.com/\"a\""),
				},
				{Type: ValueComma},
				{Type: ValueIdent, Raw: b("blue"), Value: b("blue")},
			},
		}},
	},
	{
		name: "value types",
		text: `list: a, "b", c, 7, 39%;`,
		decl: []Decl{decl("list", []Value{
			{Type: ValueIdent, Raw: b("a"), Value: b("a")},
			{Type: ValueComma},
			{Type: ValueString, Raw: b(`"b"`), Value: b("b")},
			{Type: ValueComma},
			{Type: ValueIdent, Raw: b("c"), Value: b("c")},
			{Type: ValueComma},
			{Type: ValueInteger, Raw: b("7"), Number: 7},
			{Type: ValueComma},
			{Type: ValuePercentage, Raw: b("39%"), Number: 39},
		})},
	},
	{
		name: "val_enc",
		text: `vals: 1483 1.97 19% 2.3em;`,
		decl: []Decl{{
			Property:    b("vals"),
			PropertyRaw: b("vals"),
			Values: []Value{
				{Type: ValueInteger, Raw: b("1483"), Number: 1483},
				{Type: ValueNumber, Raw: b("1.97"), Number: 1.97},
				{Type: ValuePercentage, Raw: b("19%"), Number: 19},
				{Type: ValueDimension, Raw: b("2.3em"), Number: 2.3, Value: b("em")},
			},
		}},
	},
	{
		name: "unicode ranges",
		text: `list: u+123???, u+5-f;`,
		decl: []Decl{{
			Property:    b("list"),
			PropertyRaw: b("list"),
			Values: []Value{
				{Type: ValueUnicodeRange, Raw: b("u+123???"), Value: b("u+123???")},
				{Type: ValueComma},
				{Type: ValueUnicodeRange, Raw: b("u+5-f"), Value: b("u+5-f")},
			},
		}},
	},
	{
		name: "function",
		text: `color: rgb(10, 22, 77);`,
		decl: []Decl{{
			Property:    b("color"),
			PropertyRaw: b("color"),
			Values: []Value{
				{Type: ValueFunction, Value: b("rgb"), Raw: b("rgb")},
				{Type: ValueInteger, Raw: b("10"), Number: 10},
				{Type: ValueComma},
				{Type: ValueInteger, Raw: b("22"), Number: 22},
				{Type: ValueComma},
				{Type: ValueInteger, Raw: b("77"), Number: 77},
				{Type: ValueDelim, Value: b(")")},
			},
		}},
	},
}

type parseDeclTest struct {
	name string
	text string
	decl []Decl
	pos  bool
}

var parseDeclTests = []parseDeclTest{
	{
		text: `border: 1 solid #ababab; padding: 0; background: url("https://example.com/foo.svg")`,
		decl: []Decl{
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
		text: `color: gray /* comment */; font-size: 5.67em;`,
		decl: []Decl{
			decl("color", []Value{
				{Type: ValueIdent, Raw: b("gray"), Value: b("gray")},
			}),
			decl("font-size", []Value{
				{Type: ValueDimension, Raw: b("5.67em"), Number: 5.67, Value: b("em")},
			}),
		},
	},
	{
		name: "float_notation",
		text: `list: 4.31e+9;`,
		decl: []Decl{decl("list", []Value{
			{Type: ValueNumber, Raw: b("4.31e+9"), Number: 4.31e+9},
		})},
	},
}

func TestParseDecl(t *testing.T) {
	var tests []parseDeclTest
	tests = append(tests, parseDeclTests...)
	for _, test := range parseAndFormatDeclTests {
		tests = append(tests, parseDeclTest{
			name: test.name,
			text: test.text,
			decl: test.decl,
		})
	}

	for _, test := range tests {
		name := test.name
		if name == "" {
			name = test.text
		}
		t.Run(name, func(t *testing.T) {
			errh := func(line, col, n int, msg string) {
				t.Errorf("%d:%d: (n=%d): %s", line, col, n, msg)
			}
			p := NewParser(NewScanner(strings.NewReader(test.text), errh))

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
			if !reflect.DeepEqual(got, test.decl) {
				t.Errorf("\ngot:  %v\nwant: %v", sprintDecls(got), sprintDecls(test.decl))
			}
		})
	}
}

func b(s string) []byte { return []byte(s) }

func decl(name string, v []Value) Decl {
	return Decl{
		Property:    []byte(name),
		PropertyRaw: []byte(name),
		Values:      v,
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
