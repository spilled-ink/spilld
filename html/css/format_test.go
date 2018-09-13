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
