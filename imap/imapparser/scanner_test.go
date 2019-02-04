package imapparser

import (
	"bufio"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

type tok struct {
	t Token
	v string
	s []SeqRange
}

func (t tok) String() string {
	return fmt.Sprintf("{%s %q %v}", t.t, t.v, t.s)
}

var scannerTests = []struct {
	name    string
	input   string
	expects map[int]Token
	output  []tok
	errstr  string
}{
	{
		input:  "\r\n",
		output: []tok{{t: TokenEnd}},
	},
	{
		input: `SELECT "My \"Drafts\": \\o/"` + "\r\n",
		output: []tok{
			{t: TokenAtom, v: "SELECT"},
			{t: TokenString, v: `My "Drafts": \o/`},
			{t: TokenEnd},
		},
	},
	{
		input:  `"unterminated`,
		output: []tok{},
		errstr: "unterminated string",
	},
	{
		input:  `"unterminated\`,
		output: []tok{},
		errstr: "unterminated string",
	},
	{
		input: "3 UID SEARCH 1:* NOT DELETED\r\n",
		expects: map[int]Token{
			0: TokenTag,
			3: TokenSequences,
		},
		output: []tok{
			{t: TokenTag, v: "3"}, // 0
			{t: TokenAtom, v: "UID"},
			{t: TokenAtom, v: "SEARCH"},
			{t: TokenSequences, s: []SeqRange{{Min: 1, Max: 0}}}, // 3
			{t: TokenAtom, v: "NOT"},
			{t: TokenAtom, v: "DELETED"},
			{t: TokenEnd},
		},
	},
	{
		name:   "atoms cannot contain ']'",
		input:  "[3]",
		output: []tok{},
		errstr: "invalid atom character",
	},
	{
		input: "[3] NOOP\r\n",
		expects: map[int]Token{
			0: TokenTag,
		},
		output: []tok{
			{t: TokenTag, v: "[3]"},
			{t: TokenAtom, v: "NOOP"},
			{t: TokenEnd},
		},
	},
	{
		name:  "atoms can contain '+' but tags can not",
		input: "7+ 7+\r\n",
		expects: map[int]Token{
			1: TokenTag,
		},
		output: []tok{
			{t: TokenAtom, v: "7+"},
		},
		errstr: "invalid tag character",
	},
	{
		input: "2,4:7,9,12:* 15 9:3 *\r\n",
		expects: map[int]Token{
			0: TokenSequences,
			1: TokenSequences,
			2: TokenSequences,
			3: TokenSequences,
		},
		output: []tok{
			{t: TokenSequences, s: []SeqRange{
				{Min: 2, Max: 2},
				{Min: 4, Max: 7},
				{Min: 9, Max: 9},
				{Min: 12, Max: 0},
			}},
			{t: TokenSequences, s: []SeqRange{{Min: 15, Max: 15}}},
			{t: TokenSequences, s: []SeqRange{{Min: 3, Max: 9}}},
			{t: TokenSequences, s: []SeqRange{{Min: 0, Max: 0}}},
			{t: TokenEnd},
		},
	},
	{
		name:  "short literal",
		input: "{4}\r\n💩\r\n",
		expects: map[int]Token{
			0: TokenString,
		},
		output: []tok{
			{t: TokenString, v: "💩"},
			{t: TokenEnd},
		},
	},
	{
		name:  "short literal limit",
		input: "{2048}\r\n" + string(make([]byte, 2048)) + "\r\n",
		expects: map[int]Token{
			0: TokenString,
		},
		output: []tok{},
		errstr: "greater than max 1024",
	},
}

func TestScanner(t *testing.T) {
	for _, test := range scannerTests {
		name := test.name
		if name == "" {
			name = test.input
		}
		t.Run(name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(test.input))
			f := filer.BufferFile(1024)
			defer f.Close()
			s := NewScanner(r, f, nil)
			got := []tok{}
			i := 0
			const limit = 1000
			for ; i < limit && s.Next(test.expects[i]); i++ {
				token := tok{
					t: s.Token,
					v: string(s.Value),
					s: append(([]SeqRange)(nil), s.Sequences...),
				}
				got = append(got, token)
			}
			if i == limit {
				t.Error("limit overrun")
			}
			if rem := s.buf.Buffered(); s.Error == nil && rem > 0 {
				t.Errorf("unscanned bytes, %d remaining", rem)
			}
			errstr := ""
			if s.Error != nil {
				errstr = s.Error.Error()
			}
			if !strings.Contains(errstr, test.errstr) {
				t.Errorf("scanner.Error=%q, want substring %q", s.Error, test.errstr)
			}
			if !reflect.DeepEqual(got, test.output) {
				t.Errorf("scanner\n got: %v\nwant: %v", got, test.output)
			}
			if s.Next(TokenUnknown) {
				t.Errorf("trailing token: %v", s.Token)
			}
		})
	}
}

func Test7bitPrint(t *testing.T) {
	for b := byte(0); b < 0x7f; b++ {
		if want, got := unicode.IsPrint(rune(b)), is7bitPrint(b); want != got {
			t.Errorf("is7bitPrint(%d)=%v but unicdoe.IsPrint(%d)=%v", b, got, b, want)
		}
	}
}
