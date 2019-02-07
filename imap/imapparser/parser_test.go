package imapparser

import (
	"bufio"
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"crawshaw.io/iox"
)

var parseCommandTests = []struct {
	name   string
	input  string
	mode   Mode
	output Command
	errstr string
}{
	{
		input:  "\r\n",
		errstr: "no command tag",
	},
	{
		input:  "3 FOO\r\n",
		errstr: "unknown command",
	},
	{
		input:  "0 UID FOO\r\n",
		errstr: "unknown command",
	},
	{
		input:  "0 UID LOGIN\r\n",
		errstr: "LOGIN does not support the UID prefix",
	},
	{
		input:  "0 uid login\r\n",
		errstr: "LOGIN does not support the UID prefix",
	},
	{
		input:  "0 NOOP\r\n",
		output: Command{Tag: []byte("0"), Name: "NOOP"},
	},
	{
		input:  "0 LOGIN\r\n",
		mode:   ModeAuth,
		errstr: "bad mode for command LOGIN",
	},
	{
		input:  "0 LOGIN\r\n",
		errstr: "missing username",
	},
	{
		input:  "0 LOGIN me\r\n",
		errstr: "missing password",
	},
	{
		input: "0 LOGIN me secret\r\n",
		output: Command{
			Tag:  []byte("0"),
			Name: "LOGIN",
			Auth: struct{ Username, Password []byte }{
				Username: []byte("me"),
				Password: []byte("secret"),
			},
		},
	},
	{
		input:  "0 AUTHENTICATE\r\n",
		errstr: "missing mechanism",
	},
	{
		input:  "0 AUTHENTICATE PLAIN\r\n",
		errstr: "EOF",
	},
	{
		input:  "0 AUTHENTICATE PLAIN foo\r\n",
		errstr: "has trailing arg",
	},
	{
		input: "0 AUTHENTICATE PLAIN\r\n" +
			// "FREDLAND\x00FRED FOOBAR\x00secret key"
			"RlJFRExBTkQARlJFRCBGT09CQVIAc2VjcmV0IGtleQ==\r\n",
		output: Command{
			Tag:  []byte("0"),
			Name: "AUTHENTICATE",
			Auth: struct{ Username, Password []byte }{
				Username: []byte("FRED FOOBAR"),
				Password: []byte("secret key"),
			},
		},
	},
	{
		input:  "0 ENABLE\r\n",
		mode:   ModeAuth,
		errstr: "missing required arg",
	},
	{
		input: "0 ENABLE UTF8\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:    []byte("0"),
			Name:   "ENABLE",
			Params: [][]byte{[]byte("UTF8")},
		},
	},
	{
		input:  "0 ID\r\n",
		errstr: "missing parameter list",
	},
	{
		input: "0 ID NIL\r\n",
		output: Command{
			Tag:  []byte("0"),
			Name: "ID",
		},
	},
	{
		input:  "0 ID (foo)\r\n",
		errstr: "missing value",
	},
	{
		input: `0 ID ("foo" "bar" "baz" "bop")` + "\r\n",
		output: Command{
			Tag:  []byte("0"),
			Name: "ID",
			Params: [][]byte{
				[]byte("foo"), []byte("bar"),
				[]byte("baz"), []byte("bop"),
			},
		},
	},
	{
		input: `0 ID ("foo" NIL)` + "\r\n",
		output: Command{
			Tag:    []byte("0"),
			Name:   "ID",
			Params: [][]byte{[]byte("foo"), nil},
		},
	},
	{
		input:  `0 ID (NIL bar)` + "\r\n",
		errstr: "NIL field name",
	},
	{input: "0 SELECT\r\n", mode: ModeAuth, errstr: "missing mailbox"},
	{input: "0 EXAMINE\r\n", mode: ModeAuth, errstr: "missing mailbox"},
	{
		input: "0 SELECT inbox\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:     []byte("0"),
			Name:    "SELECT",
			Mailbox: []byte("INBOX"),
		},
	},
	{
		input: "0 SELECT inbox (CONDSTORE)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:       []byte("0"),
			Name:      "SELECT",
			Mailbox:   []byte("INBOX"),
			Condstore: true,
		},
	},
	{
		input: "A02 SELECT INBOX (QRESYNC (67890007 20050715194045000 41,43:211,214:541))\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:     []byte("A02"),
			Name:    "SELECT",
			Mailbox: []byte("INBOX"),
			Qresync: QresyncParam{
				UIDValidity: 67890007,
				ModSeq:      20050715194045000,
				UIDs: []SeqRange{
					{Min: 41, Max: 41},
					{Min: 43, Max: 211},
					{Min: 214, Max: 541},
				},
			},
		},
	},
	{
		input: "A03 SELECT INBOX (QRESYNC (67890007 90060115194045000 41:211,214:541))\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:     []byte("A03"),
			Name:    "SELECT",
			Mailbox: []byte("INBOX"),
			Qresync: QresyncParam{
				UIDValidity: 67890007,
				ModSeq:      90060115194045000,
				UIDs: []SeqRange{
					{Min: 41, Max: 211},
					{Min: 214, Max: 541},
				},
			},
		},
	},
	{
		input: "B04 SELECT INBOX (QRESYNC (67890007 90060115194045000 1:29997 (" +
			"5000,7500,9000,9990:9999 " +
			"15000,22500,27000,29970,29973,29976,29979,29982,29985,29988,29991,29994,29997" +
			")))\r\n",
		mode: ModeAuth,
		output: Command{
			Tag:     []byte("B04"),
			Name:    "SELECT",
			Mailbox: []byte("INBOX"),
			Qresync: QresyncParam{
				UIDValidity: 67890007,
				ModSeq:      90060115194045000,
				UIDs:        []SeqRange{{Min: 1, Max: 29997}},
				KnownSeqNumMatch: []SeqRange{
					{Min: 5000, Max: 5000},
					{Min: 7500, Max: 7500},
					{Min: 9000, Max: 9000},
					{Min: 9990, Max: 9999},
				},
				KnownUIDMatch: []SeqRange{
					{Min: 15000, Max: 15000},
					{Min: 22500, Max: 22500},
					{Min: 27000, Max: 27000},
					{Min: 29970, Max: 29970},
					{Min: 29973, Max: 29973},
					{Min: 29976, Max: 29976},
					{Min: 29979, Max: 29979},
					{Min: 29982, Max: 29982},
					{Min: 29985, Max: 29985},
					{Min: 29988, Max: 29988},
					{Min: 29991, Max: 29991},
					{Min: 29994, Max: 29994},
					{Min: 29997, Max: 29997},
				},
			},
		},
	},

	{
		input: "0 EXAMINE INBOX (CONDSTORE)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:       []byte("0"),
			Name:      "EXAMINE",
			Mailbox:   []byte("INBOX"),
			Condstore: true,
		},
	},
	{
		input:  "0 RENAME\r\n",
		mode:   ModeAuth,
		errstr: "missing existing mailbox name",
	},
	{
		input:  "0 RENAME inbox\r\n",
		mode:   ModeAuth,
		errstr: "missing new mailbox name",
	},
	{
		input: "0 RENAME inbox old-mail\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("0"),
			Name: "RENAME",
			Rename: struct{ OldMailbox, NewMailbox []byte }{
				OldMailbox: []byte("INBOX"),
				NewMailbox: []byte("old-mail"),
			},
		},
	},
	{
		input:  "0 LIST\r\n",
		mode:   ModeNonAuth,
		errstr: "bad mode for command LIST",
	},
	{
		input:  "0 LIST \r\n",
		mode:   ModeAuth,
		errstr: "EOF",
	},
	{
		input:  "0 LIST (SUBSCRIBED)\r\n",
		mode:   ModeAuth,
		errstr: "missing reference name",
	},
	{
		input:  "0 LIST a\r\n",
		mode:   ModeAuth,
		errstr: "missing mailbox glob",
	},
	{
		input:  "0 LIST (SUBSCRIBED) a\r\n",
		mode:   ModeAuth,
		errstr: "missing mailbox glob",
	},
	{
		input: "4.2 LIST \"\" *\r\n", // from macOS Mail.app
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("4.2"),
			Name: "LIST",
			List: List{
				ReferenceName: []byte(""),
				MailboxGlob:   []byte("*"),
			},
		},
	},
	{
		input: "4.2 LIST \"\" \"*\"\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("4.2"),
			Name: "LIST",
			List: List{
				ReferenceName: []byte(""),
				MailboxGlob:   []byte("*"),
			},
		},
	},
	{
		input: `0 LIST ~smith/Mail/ "foo.*"` + "\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("0"),
			Name: "LIST",
			List: List{
				ReferenceName: []byte("~smith/Mail/"),
				MailboxGlob:   []byte("foo.*"),
			},
		},
	},
	{
		input: "a LIST (REMOTE SUBSCRIBED) \"/\" \"*\" RETURN (CHILDREN)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("a"),
			Name: "LIST",
			List: List{
				SelectOptions: []string{"REMOTE", "SUBSCRIBED"},
				ReturnOptions: []string{"CHILDREN"},
				ReferenceName: []byte("/"),
				MailboxGlob:   []byte("*"),
			},
		},
	},
	{
		input: "a LIST \"/\" \"*\" RETURN (CHILDREN)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("a"),
			Name: "LIST",
			List: List{
				ReturnOptions: []string{"CHILDREN"},
				ReferenceName: []byte("/"),
				MailboxGlob:   []byte("*"),
			},
		},
	},
	{
		input: "t2 LIST \"\" \"%\" RETURN (SPECIAL-USE)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("t2"),
			Name: "LIST",
			List: List{
				ReturnOptions: []string{"SPECIAL-USE"},
				ReferenceName: []byte(""),
				MailboxGlob:   []byte("%"),
			},
		},
	},
	{
		input: "t3 LIST (SPECIAL-USE) \"\" \"*\"\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("t3"),
			Name: "LIST",
			List: List{
				SelectOptions: []string{"SPECIAL-USE"},
				ReferenceName: []byte(""),
				MailboxGlob:   []byte("*"),
			},
		},
	},
	{
		input:  "0 EXPUNGE\r\n",
		mode:   ModeNonAuth,
		errstr: "bad mode",
	},
	{
		input: "0 EXPUNGE\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("0"),
			Name: "EXPUNGE",
		},
	},
	{
		input:  "0 EXPUNGE 1:2\r\n",
		mode:   ModeSelected,
		errstr: "trailing arguments",
	},
	{
		input: "0 UID EXPUNGE 1:2\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("0"),
			UID:       true,
			Name:      "EXPUNGE",
			Sequences: []SeqRange{{Min: 1, Max: 2}},
		},
	},
	{
		input: "3 SEARCH UNSEEN\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:    []byte("3"),
			Name:   "SEARCH",
			Search: Search{Op: &SearchOp{Key: "UNSEEN"}},
		},
	},
	{
		input:  "3 SEARCH\r\n",
		mode:   ModeSelected,
		errstr: "missing search key",
	},
	{
		input:  "3 SEARCH CHARSET\r\n",
		mode:   ModeSelected,
		errstr: "missing CHARSET value",
	},
	{
		input:  "3 SEARCH CHARSET UTF-99\r\n",
		mode:   ModeSelected,
		errstr: "unsupported CHARSET",
	},
	{
		input:  "3 SEARCH CHARSET UTF-99 UNSEEN\r\n",
		mode:   ModeSelected,
		errstr: "unsupported CHARSET",
	},
	{
		input:  "3 SEARCH NOT\r\n",
		mode:   ModeSelected,
		errstr: "NOT missing term",
	},
	{
		input:  "3 SEARCH OR\r\n",
		mode:   ModeSelected,
		errstr: "OR missing first",
	},
	{
		input:  "3 SEARCH OR SEEN\r\n",
		mode:   ModeSelected,
		errstr: "OR missing second",
	},
	{
		input: "3 UID SEARCH 1:* NOT DELETED\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("3"),
			Name: "SEARCH",
			UID:  true,
			Search: Search{Op: &SearchOp{
				Key: "AND",
				Children: []SearchOp{
					{
						Key:       "SEQSET",
						Sequences: []SeqRange{{Min: 1, Max: 0}},
					},
					{
						Key:      "NOT",
						Children: []SearchOp{{Key: "DELETED"}},
					},
				},
			}},
		},
	},
	{
		input: "3 uid search ( 1:* Or not deleted Not Seen )\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("3"),
			Name: "SEARCH",
			UID:  true,
			Search: Search{Op: &SearchOp{
				Key: "AND",
				Children: []SearchOp{
					{
						Key:       "SEQSET",
						Sequences: []SeqRange{{Min: 1, Max: 0}},
					},
					{
						Key: "OR",
						Children: []SearchOp{
							{Key: "NOT", Children: []SearchOp{{Key: "DELETED"}}},
							{Key: "NOT", Children: []SearchOp{{Key: "SEEN"}}},
						},
					},
				},
			}},
		},
	},
	{
		input: "7 SEARCH uid 3:19\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("7"),
			Name: "SEARCH",
			Search: Search{Op: &SearchOp{
				Key:       "UID",
				Sequences: []SeqRange{{Min: 3, Max: 19}},
			}},
		},
	},
	{
		input: "3 SEARCH RETURN (COUNT ALL) UNSEEN\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("3"),
			Name: "SEARCH",
			Search: Search{
				Return: []string{"COUNT", "ALL"},
				Op:     &SearchOp{Key: "UNSEEN"},
			},
		},
	},
	{
		input: "3 SEARCH RETURN () UNSEEN\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("3"),
			Name: "SEARCH",
			Search: Search{
				Return: []string{"ALL"},
				Op:     &SearchOp{Key: "UNSEEN"},
			},
		},
	},
	{
		input: "7 UID SEARCH RETURN (COUNT) 1:* NOT DELETED\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("7"),
			Name: "SEARCH",
			UID:  true,
			Search: Search{
				Return: []string{"COUNT"},
				Op: &SearchOp{
					Key: "AND",
					Children: []SearchOp{
						{Key: "SEQSET", Sequences: []SeqRange{{Min: 1, Max: 0}}},
						{Key: "NOT", Children: []SearchOp{{Key: "DELETED"}}},
					},
				},
			},
		},
	},
	{
		input: "7 search new old recent seen\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("7"),
			Name: "SEARCH",
			Search: Search{Op: &SearchOp{
				Key: "AND",
				Children: []SearchOp{
					{Key: "NEW"},
					{Key: "OLD"},
					{Key: "RECENT"},
					{Key: "SEEN"},
				},
			}},
		},
	},
	{
		input: "a0x04 SEARCH TO foo\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:    []byte("a0x04"),
			Name:   "SEARCH",
			Search: Search{Op: &SearchOp{Key: "TO", Value: `foo`}},
		},
	},
	{
		input: `a SEARCH TO "foo \"bar\\baz\""` + "\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:    []byte("a"),
			Name:   "SEARCH",
			Search: Search{Op: &SearchOp{Key: "TO", Value: `foo "bar\baz"`}},
		},
	},
	{
		input: "a SEARCH TO {7}\r\nfoo\nbar\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:    []byte("a"),
			Name:   "SEARCH",
			Search: Search{Op: &SearchOp{Key: "TO", Value: "foo\nbar"}},
		},
	},
	{
		// An astonishing little query produced by the inbox load of iOS Mail.
		input: `5 SEARCH (OR HEADER Message-ID "<prod123@example.com>" HEADER Message-ID "<prod456@example.com>") NOT DELETED` + "\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("5"),
			Name: "SEARCH",
			Search: Search{Op: &SearchOp{Key: "AND", Children: []SearchOp{
				{
					Key: "OR",
					Children: []SearchOp{
						{Key: "HEADER", Value: "Message-ID: <prod123@example.com>"},
						{Key: "HEADER", Value: "Message-ID: <prod456@example.com>"},
					},
				},
				{
					Key:      "NOT",
					Children: []SearchOp{{Key: "DELETED"}},
				},
			}}},
		},
	},
	{
		input: "a SEARCH BEFORE 12-Feb-1999\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("a"),
			Name: "SEARCH",
			Search: Search{Op: &SearchOp{
				Key:  "BEFORE",
				Date: time.Date(1999, time.February, 12, 0, 0, 0, 0, time.UTC),
			}},
		},
	},
	{
		input:  "a SEARCH ON 12-1-1989\r\n",
		mode:   ModeSelected,
		errstr: "missing date",
	},
	{
		input: "a SEARCH MODSEQ \"/flags/\\\\draft\" all 620162338\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("a"),
			Name: "SEARCH",
			Search: Search{Op: &SearchOp{
				Key: "MODSEQ",
				Num: 620162338,
			}},
		},
	},
	{
		input: "a SEARCH MODSEQ 42\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:    []byte("a"),
			Name:   "SEARCH",
			Search: Search{Op: &SearchOp{Key: "MODSEQ", Num: 42}},
		},
	},
	{
		input: "t SEARCH OR NOT MODSEQ 720162338 LARGER 50000\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("t"),
			Name: "SEARCH",
			Search: Search{Op: &SearchOp{
				Key: "OR",
				Children: []SearchOp{
					{
						Key:      "NOT",
						Children: []SearchOp{{Key: "MODSEQ", Num: 720162338}},
					},
					{
						Key: "LARGER",
						Num: 50000,
					},
				},
			}},
		},
	},
	{
		input: "t SEARCH SMALLER 50\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:  []byte("t"),
			Name: "SEARCH",
			Search: Search{
				Op: &SearchOp{Key: "SMALLER", Num: 50},
			},
		},
	},
	{
		input: "t01 APPEND INBOX {5}\r\nHello\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:     []byte("t01"),
			Name:    "APPEND",
			Mailbox: []byte("INBOX"),
			Literal: literal("Hello"),
		},
	},
	{
		input: "t02 APPEND saved (\\Seen) {5}\r\nHello\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:     []byte("t02"),
			Name:    "APPEND",
			Mailbox: []byte("saved"),
			Literal: literal("Hello"),
			Append: struct {
				Flags [][]byte
				Date  []byte
			}{
				Flags: [][]byte{[]byte("\\Seen")},
			},
		},
	},
	{
		input: "t02 APPEND saved (\\Seen) \"30-10-2018 11:11:11 +1000\" {5}\r\nHello\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:     []byte("t02"),
			Name:    "APPEND",
			Mailbox: []byte("saved"),
			Literal: literal("Hello"),
			Append: struct {
				Flags [][]byte
				Date  []byte
			}{
				Flags: [][]byte{[]byte("\\Seen")},
				Date:  []byte("30-10-2018 11:11:11 +1000"),
			},
		},
	},
	{
		name:  "long literal",
		input: "t01 APPEND \"Drafts\" {1029}\r\nHello" + strings.Repeat("_", 1024) + "\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:     []byte("t01"),
			Name:    "APPEND",
			Mailbox: []byte("Drafts"),
			Literal: literal("Hello" + strings.Repeat("_", 1024)),
		},
	},
	{
		input: "01 STATUS MyMsgs (MESSAGES RECENT UNSEEN)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:     []byte("01"),
			Name:    "STATUS",
			Mailbox: []byte("MyMsgs"),
			Status: struct{ Items []StatusItem }{
				Items: []StatusItem{StatusMessages, StatusRecent, StatusUnseen},
			},
		},
	},
	{
		input: "01 STATUS \"~peter/mail/&U,BTFw-/&ZeVnLIqe-\" (MESSAGES RECENT UNSEEN)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:     []byte("01"),
			Name:    "STATUS",
			Mailbox: []byte("~peter/mail/台北/日本語"),
			Status: struct{ Items []StatusItem }{
				Items: []StatusItem{StatusMessages, StatusRecent, StatusUnseen},
			},
		},
	},
	{
		input:  `01 STATUS INBOX\r\n`,
		mode:   ModeAuth,
		errstr: "STATUS missing list start",
	},
	{
		input:  "0 FETCH\r\n",
		mode:   ModeNonAuth,
		errstr: "bad mode for command FETCH",
	},
	{
		input: "1 FETCH 1:* ALL\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:        []byte("1"),
			Name:       "FETCH",
			Sequences:  []SeqRange{{1, 0}},
			FetchItems: []FetchItem{{Type: FetchAll}},
		},
	},
	{
		input:  "1 FETCH 1:1 (ALL)\r\n",
		mode:   ModeSelected,
		errstr: "invalid item",
	},
	{
		input: "A FETCH 4,9,16:* (INTERNALDATE)\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:        []byte("A"),
			Name:       "FETCH",
			Sequences:  []SeqRange{{4, 4}, {9, 9}, {16, 0}},
			FetchItems: []FetchItem{{Type: FetchInternalDate}},
		},
	},
	{
		input: "t FETCH 260 BODY.PEEK[1]<2187.1>\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("t"),
			Name:      "FETCH",
			Sequences: []SeqRange{{260, 260}},
			FetchItems: []FetchItem{{
				Type: FetchBody,
				Peek: true,
				Section: FetchItemSection{
					Path: []uint16{1},
				},
				Partial: struct{ Start, Length uint32 }{
					Start:  2187,
					Length: 1,
				},
			}},
		},
	},
	{
		input: "t FETCH 260 BODY.PEEK[1]<2187.1>\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("t"),
			Name:      "FETCH",
			Sequences: []SeqRange{{260, 260}},
			FetchItems: []FetchItem{{
				Type: FetchBody,
				Peek: true,
				Section: FetchItemSection{
					Path: []uint16{1},
				},
				Partial: struct{ Start, Length uint32 }{
					Start:  2187,
					Length: 1,
				},
			}},
		},
	},
	{
		input: "t FETCH 1 (BODY[4.1.MIME] BODY[4.2.HEADER])\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("t"),
			Name:      "FETCH",
			Sequences: []SeqRange{{1, 1}},
			FetchItems: []FetchItem{
				{
					Type: FetchBody,
					Section: FetchItemSection{
						Path: []uint16{4, 1},
						Name: "MIME",
					},
				},
				{
					Type: FetchBody,
					Section: FetchItemSection{
						Path: []uint16{4, 2},
						Name: "HEADER",
					},
				},
			},
		},
	},
	{
		input: "A654 FETCH 2:4 (FLAGS BODY[HEADER.FIELDS (DATE FROM)])\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("A654"),
			Name:      "FETCH",
			Sequences: []SeqRange{{2, 4}},
			FetchItems: []FetchItem{
				{Type: FetchFlags},
				{
					Type: FetchBody,
					Section: FetchItemSection{
						Name: "HEADER.FIELDS",
						Headers: [][]byte{
							[]byte("DATE"),
							[]byte("FROM"),
						},
					},
				},
			},
		},
	},
	{
		input: "TAG UID FETCH 266:270 (INTERNALDATE UID RFC822.SIZE FLAGS BODY.PEEK[HEADER.FIELDS (date subject from content-type to cc bcc message-id in-reply-to references list-id)])\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("TAG"),
			UID:       true,
			Name:      "FETCH",
			Sequences: []SeqRange{{266, 270}},
			FetchItems: []FetchItem{
				{Type: FetchInternalDate},
				{Type: FetchUID},
				{Type: FetchRFC822Size},
				{Type: FetchFlags},
				{
					Type: FetchBody,
					Peek: true,
					Section: FetchItemSection{
						Name: "HEADER.FIELDS",
						Headers: [][]byte{
							[]byte("date"),
							[]byte("subject"),
							[]byte("from"),
							[]byte("content-type"),
							[]byte("to"),
							[]byte("cc"),
							[]byte("bcc"),
							[]byte("message-id"),
							[]byte("in-reply-to"),
							[]byte("references"),
							[]byte("list-id"),
						},
					},
				},
			},
		},
	},
	{
		input: "8.277 UID FETCH 279 (BODY.PEEK[2.19] BODY.PEEK[2.13]<32342.88162> BODY.PEEK[2.21])\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("8.277"),
			UID:       true,
			Name:      "FETCH",
			Sequences: []SeqRange{{279, 279}},
			FetchItems: []FetchItem{
				{
					Type:    FetchBody,
					Peek:    true,
					Section: FetchItemSection{Path: []uint16{2, 19}},
				},
				{
					Type:    FetchBody,
					Peek:    true,
					Section: FetchItemSection{Path: []uint16{2, 13}},
					Partial: struct{ Start, Length uint32 }{
						Start:  32342,
						Length: 88162,
					},
				},
				{
					Type:    FetchBody,
					Peek:    true,
					Section: FetchItemSection{Path: []uint16{2, 21}},
				},
				{Type: FetchUID}, // implicitly included
			},
		},
	},
	{
		input: "s100 FETCH 1:* (FLAGS) (CHANGEDSINCE 12345)\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:          []byte("s100"),
			Name:         "FETCH",
			Sequences:    []SeqRange{{1, 0}},
			FetchItems:   []FetchItem{{Type: FetchFlags}},
			ChangedSince: 12345,
		},
	},
	{
		input: "s100 FETCH 300:500 (FLAGS) (CHANGEDSINCE 12345 VANISHED)\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:          []byte("s100"),
			Name:         "FETCH",
			Sequences:    []SeqRange{{300, 500}},
			FetchItems:   []FetchItem{{Type: FetchFlags}},
			ChangedSince: 12345,
			Vanished:     true,
		},
	},
	{
		input: "A003 STORE 2:4 +FLAGS (\\Deleted)\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("A003"),
			Name:      "STORE",
			Sequences: []SeqRange{{2, 4}},
			Store: Store{
				Mode:  StoreAdd,
				Flags: [][]byte{[]byte("\\Deleted")},
			},
		},
	},
	{
		input:  "TAG STORE 2:4 boo (\\Deleted)\r\n",
		mode:   ModeSelected,
		errstr: "invalid name",
	},
	{
		input: "d105 STORE 7,5,9 (UNCHANGEDSINCE 320162338) +FLAGS.SILENT (\\Deleted)\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("d105"),
			Name:      "STORE",
			Sequences: []SeqRange{{7, 7}, {5, 5}, {9, 9}},
			Store: Store{
				Mode:           StoreAdd,
				Silent:         true,
				Flags:          [][]byte{[]byte("\\Deleted")},
				UnchangedSince: 320162338,
			},
		},
	},
	{
		input: "A003 COPY 2:4 MEETING\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("A003"),
			Name:      "COPY",
			Sequences: []SeqRange{{2, 4}},
			Mailbox:   []byte("MEETING"),
		},
	},
	{
		input: "A003 UID MOVE 2:4 MEETING\r\n",
		mode:  ModeSelected,
		output: Command{
			Tag:       []byte("A003"),
			UID:       true,
			Name:      "MOVE",
			Sequences: []SeqRange{{2, 4}},
			Mailbox:   []byte("MEETING"),
		},
	},
	{
		input: "1 XAPPLEPUSHSERVICE aps-version 2 aps-account-id 1111111D-A111-A111-A111-111111111111 aps-device-token 1111111111111122222222222233333333333444444444444555555555566666 aps-subtopic com.apple.mobilemail mailboxes (INBOX Notes)\r\n",
		mode:  ModeAuth,
		output: Command{
			Tag:  []byte("1"),
			Name: "XAPPLEPUSHSERVICE",
			ApplePushService: &ApplePushService{
				Mailboxes: []string{"INBOX", "Notes"},
				Version:   2,
				Device: ApplePushDevice{
					AccountID:   "1111111D-A111-A111-A111-111111111111",
					DeviceToken: "1111111111111122222222222233333333333444444444444555555555566666",
				},
				Subtopic: "com.apple.mobilemail",
			},
		},
	},
}

func literal(contents string) *iox.BufferFile {
	f := filer.BufferFile(0)
	if _, err := io.WriteString(f, contents); err != nil {
		panic(err)
	}
	return f
}

var filer = iox.NewFiler(0)

func TestParseCommand(t *testing.T) {
	for _, test := range parseCommandTests {
		name := test.name
		if name == "" {
			name = test.input
		}
		t.Run(name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(test.input))
			f := filer.BufferFile(1024)
			defer f.Close()
			p := &Parser{
				Scanner: NewScanner(r, f, nil),
				Mode:    test.mode,
			}
			err := p.ParseCommand()
			if err != nil {
				t.Logf("err=%v", err)
				errstr := err.Error()
				if !strings.Contains(errstr, test.errstr) {
					t.Errorf("parse error %q, want substring %q", errstr, test.errstr)
				}
				if test.errstr == "" {
					t.Errorf("unexpected parse error: %v", err)
				} else {
					if _, err := r.Peek(1); err != io.EOF {
						t.Errorf("unconsumed buffer on error: %d", r.Buffered())
					}
				}
				if p.Command.Name == "" {
					return
				}
			}
			if !equalCommand(p.Command, test.output) {
				t.Errorf("ParseCommand=\n\t%v\n, want\n\t%v", p.Command, test.output)
			}
		})
	}
}

func equalSeqRange(s0, s1 []SeqRange) bool {
	if len(s0) == 0 && len(s1) == 0 {
		return true
	}
	return reflect.DeepEqual(s0, s1)
}

func equalCommand(c0, c1 Command) bool {
	if !bytes.Equal(c0.Tag, c1.Tag) {
		return false
	}
	if c0.Name != c1.Name {
		return false
	}
	if c0.UID != c1.UID {
		return false
	}
	if !bytes.Equal(c0.Mailbox, c1.Mailbox) {
		return false
	}
	if c0.Condstore != c1.Condstore {
		return false
	}
	if c0.Qresync.UIDValidity != c1.Qresync.UIDValidity {
		return false
	}
	if c0.Qresync.ModSeq != c1.Qresync.ModSeq {
		return false
	}
	if !equalSeqRange(c0.Qresync.UIDs, c0.Qresync.UIDs) {
		return false
	}
	if !equalSeqRange(c0.Qresync.KnownSeqNumMatch, c0.Qresync.KnownSeqNumMatch) {
		return false
	}
	if !equalSeqRange(c0.Qresync.KnownUIDMatch, c0.Qresync.KnownUIDMatch) {
		return false
	}
	if !equalSeqRange(c0.Sequences, c1.Sequences) {
		return false
	}
	if c0.Literal != nil || c1.Literal != nil {
		var c0len, c1len int64
		if c0.Literal != nil {
			c0len = c0.Literal.Size()
		}
		if c1.Literal != nil {
			c1len = c1.Literal.Size()
		}
		if c0len != c1len {
			return false
		}
		if c0len != 0 {
			r0 := io.NewSectionReader(c0.Literal, 0, c0.Literal.Size())
			b0, err := ioutil.ReadAll(r0)
			if err != nil {
				return false
			}
			r1 := io.NewSectionReader(c1.Literal, 0, c1.Literal.Size())
			b1, err := ioutil.ReadAll(r1)
			if err != nil {
				return false
			}
			if !bytes.Equal(b0, b1) {
				return false
			}
		}
	}
	if !bytes.Equal(c0.Rename.OldMailbox, c1.Rename.OldMailbox) {
		return false
	}
	if !bytes.Equal(c0.Rename.NewMailbox, c1.Rename.NewMailbox) {
		return false
	}
	if len(c0.Params) > 0 || len(c1.Params) > 0 {
		if !reflect.DeepEqual(c0.Params, c1.Params) {
			return false
		}
	}
	if !bytes.Equal(c0.Auth.Username, c1.Auth.Username) {
		return false
	}
	if !bytes.Equal(c0.Auth.Password, c1.Auth.Password) {
		return false
	}
	if len(c0.List.SelectOptions) > 0 || len(c1.List.SelectOptions) > 0 {
		if !reflect.DeepEqual(c0.List.SelectOptions, c1.List.SelectOptions) {
			return false
		}
	}
	if !bytes.Equal(c0.List.MailboxGlob, c1.List.MailboxGlob) {
		return false
	}
	if !bytes.Equal(c0.List.ReferenceName, c1.List.ReferenceName) {
		return false
	}
	if len(c0.List.ReturnOptions) > 0 || len(c1.List.ReturnOptions) > 0 {
		if !reflect.DeepEqual(c0.List.ReturnOptions, c1.List.ReturnOptions) {
			return false
		}
	}
	if len(c0.Status.Items) > 0 || len(c1.Status.Items) > 0 {
		if !reflect.DeepEqual(c0.Status.Items, c1.Status.Items) {
			return false
		}
	}
	if len(c0.Append.Flags) > 0 || len(c1.Append.Flags) > 0 {
		if !reflect.DeepEqual(c0.Append.Flags, c1.Append.Flags) {
			return false
		}
	}
	if !bytes.Equal(c0.Append.Date, c1.Append.Date) {
		return false
	}
	if len(c0.FetchItems) > 0 || len(c1.FetchItems) > 0 {
		if !reflect.DeepEqual(c0.FetchItems, c1.FetchItems) {
			return false
		}
	}
	if c0.ChangedSince != c1.ChangedSince {
		return false
	}
	if c0.Vanished != c1.Vanished {
		return false
	}
	if c0.Store.Mode != c1.Store.Mode {
		return false
	}
	if c0.Store.UnchangedSince != c1.Store.UnchangedSince {
		return false
	}
	if len(c0.Store.Flags) > 0 || len(c1.Store.Flags) > 0 {
		if !reflect.DeepEqual(c0.Store.Flags, c1.Store.Flags) {
			return false
		}
	}
	if !reflect.DeepEqual(c0.Search.Op, c1.Search.Op) {
		return false
	}
	if c0.Search.Charset != c1.Search.Charset {
		return false
	}
	if c0.ApplePushService != nil || c1.ApplePushService != nil {
		if c0.ApplePushService == nil || c1.ApplePushService == nil {
			return false
		}
		if len(c0.ApplePushService.Mailboxes) > 0 || len(c1.ApplePushService.Mailboxes) > 0 {
			if !reflect.DeepEqual(c0.ApplePushService.Mailboxes, c1.ApplePushService.Mailboxes) {
				return false
			}
		}
		if c0.ApplePushService.Version != c1.ApplePushService.Version {
			return false
		}
		if c0.ApplePushService.Device != c1.ApplePushService.Device {
			return false
		}
		if c0.ApplePushService.Subtopic != c1.ApplePushService.Subtopic {
			return false
		}
	}
	return true
}

func TestLiteralContinuationFunc(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	cont := make(chan string)
	contFn := func(msg string, len uint32) {
		if !strings.HasPrefix(msg, "+ ") {
			t.Errorf(`continuation message %q missing "+ " prefix`, msg)
		}
		if !strings.HasSuffix(msg, "\r\n") {
			t.Errorf("continuation message %q missing CRLF", msg)
		}
		cont <- msg
	}

	f := filer.BufferFile(1024)
	defer f.Close()

	p := &Parser{
		Scanner: NewScanner(bufio.NewReader(r), f, contFn),
	}
	parseErr := make(chan error)
	go func() {
		parseErr <- p.ParseCommand()
	}()

	if _, err := w.WriteString("A001 LOGIN {11}\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cont:
	case err := <-parseErr:
		t.Fatalf("parse error before username: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout before username")
	}
	if _, err := w.WriteString("FRED FOOBAR {7}\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cont:
	case err := <-parseErr:
		t.Fatalf("parse error before password: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout before password")
	}
	if _, err := w.WriteString("fat man\r\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-parseErr:
		// At this point we should expect a nil err.
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for parse")
	}

	want := Command{
		Tag:  []byte("A001"),
		Name: "LOGIN",
		Auth: struct{ Username, Password []byte }{
			Username: []byte("FRED FOOBAR"),
			Password: []byte("fat man"),
		},
	}

	if !equalCommand(p.Command, want) {
		t.Errorf("got:\n\t%s\n\t%s", p.Command, want)
	}
}

func TestAuthPlainContinuation(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	cont := make(chan string)
	contFn := func(msg string, len uint32) {
		if !strings.HasPrefix(msg, "+ ") {
			t.Errorf(`continuation message %q missing "+ " prefix`, msg)
		}
		if !strings.HasSuffix(msg, "\r\n") {
			t.Errorf("continuation message %q missing CRLF", msg)
		}
		cont <- msg
	}

	f := filer.BufferFile(1024)
	defer f.Close()

	p := &Parser{
		Scanner: NewScanner(bufio.NewReader(r), f, contFn),
	}
	parseErr := make(chan error)
	go func() {
		parseErr <- p.ParseCommand()
	}()

	if _, err := w.WriteString("a001 AUTHENTICATE \"PLAIN\"\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cont:
	case err := <-parseErr:
		t.Fatalf("parse error after PLAIN: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout after PLAIN")
	}
	if _, err := w.WriteString("AEZSRUQgRk9PQkFSAGEgc2VjcmV0IGtleQ==\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-parseErr:
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for parse")
	}

	want := Command{
		Tag:  []byte("a001"),
		Name: "AUTHENTICATE",
		Auth: struct{ Username, Password []byte }{
			Username: []byte("FRED FOOBAR"),
			Password: []byte("a secret key"),
		},
	}

	if !equalCommand(p.Command, want) {
		t.Errorf("got:\n\t%s\n\t%s", p.Command, want)
	}
}
