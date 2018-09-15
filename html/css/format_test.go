package css

import (
	"testing"
)

var formatDeclTests = []struct {
	name string
	decl Decl
	want string
}{
	{
		name: "url_encoding",
		decl: Decl{
			Property: b("background"),
			Values: []Value{
				{Type: ValueURL, Value: b("https://example.com/\"a\"")},
				{Type: ValueComma},
				{Type: ValueIdent, Value: b("blue")},
			},
		},
		want: `background: url("https://example.com/\"a\""), blue;`,
	},
	{
		name: "url_encoding",
		decl: Decl{
			Property: b("vals"),
			Values: []Value{
				{Type: ValueInteger, Number: 1483},
				{Type: ValueNumber, Number: 1.97},
				{Type: ValuePercentage, Number: 19},
				{Type: ValueDimension, Number: 2.3, Value: b("em")},
			},
		},
		want: `vals: 1483 1.97 19% 2.3em;`,
	},
}

func TestAppendDecl(t *testing.T) {
	for _, test := range formatDeclTests {
		t.Run(test.name, func(t *testing.T) {
			got := string(AppendDecl(nil, &test.decl))
			if got != test.want {
				t.Errorf(" got: %q\nwant: %q", got, test.want)
			}
		})
	}
}
