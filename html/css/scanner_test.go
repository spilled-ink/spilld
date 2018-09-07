package css

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type token struct {
	tok Token
	lit string
}

func (t token) String() string { return fmt.Sprintf("{token:%s %q}", t.tok, t.lit) }

var scannerTests = []struct {
	input string
	want  []token
}{
	{
		input: `img  { foo: "Hello, 世界"  /* not a real rule */ }`,
		want: []token{
			{Ident, "img"},
			{LeftBrace, ""},
			{Ident, "foo"},
			{Colon, ""},
			{String, "Hello, 世界"},
			{RightBrace, ""},
			{EOF, ""},
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
				println("calling Next")
				s.Next()
				got = append(got, token{tok: s.Token, lit: string(s.Literal)})
				println(fmt.Sprintf("token=%v", s.Token))
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
