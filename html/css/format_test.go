package css

import (
	"testing"
)

func TestAppendDecl(t *testing.T) {
	for _, test := range parseAndFormatDeclTests {
		t.Run(test.name, func(t *testing.T) {
			got := string(AppendDecl(nil, &test.decl[0]))
			if got != test.text {
				t.Errorf("\n got: %q\nwant: %q", got, test.text)
			}
		})
	}
}
