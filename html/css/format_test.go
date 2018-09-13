package css

import (
	"bytes"
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

func TestFormatDecl(t *testing.T) {
	for _, test := range formatDeclTests {
		t.Run(test.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			FormatDecl(buf, &test.decl)
			got := buf.String()
			if got != test.want {
				t.Errorf("FormatDecl:\n  got: %q\n want: %q", got, test.want)
			}
		})
	}
}
