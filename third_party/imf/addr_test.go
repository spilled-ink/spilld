package imf

import (
	"reflect"
	"strings"
	"testing"

	"spilled.ink/email"
)

func TestAddressParsingError(t *testing.T) {
	mustErrTestCases := [...]struct {
		text        string
		wantErrText string
	}{
		0:  {"group: first@example.com, second@example.com;", "group with multiple addresses"},
		1:  {"a@gmail.com b@gmail.com", "expected single address"},
		2:  {string([]byte{0xed, 0xa0, 0x80}) + " <micro@example.net>", "invalid utf-8 in address"},
		3:  {"\"" + string([]byte{0xed, 0xa0, 0x80}) + "\" <half-surrogate@example.com>", "invalid utf-8 in quoted-string"},
		4:  {"\"\\" + string([]byte{0x80}) + "\" <escaped-invalid-unicode@example.net>", "invalid utf-8 in quoted-string"},
		5:  {"\"\x00\" <null@example.net>", "bad character in quoted-string"},
		6:  {"\"\\\x00\" <escaped-null@example.net>", "bad character in quoted-string"},
		7:  {"John Doe", "no angle-addr"},
		8:  {`<jdoe#machine.example>`, "missing @ in addr-spec"},
		9:  {`John <middle> Doe <jdoe@machine.example>`, "missing @ in addr-spec"},
		10: {"cfws@example.com (", "misformatted parenthetical comment"},
		11: {"empty group: ;", "empty group"},
		12: {"root group: embed group: null@example.com;", "no angle-addr"},
		13: {"group not closed: null@example.com", "expected comma"},
	}

	for i, tc := range mustErrTestCases {
		_, err := ParseAddress(tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.wantErrText) {
			t.Errorf(`mail.ParseAddress(%q) #%d want %q, got %v`, tc.text, i, tc.wantErrText, err)
		}
	}
}

func TestAddressParsing(t *testing.T) {
	tests := []struct {
		addrsStr string
		exp      []*email.Address
	}{
		// Bare address
		{
			`jdoe@machine.example`,
			[]*email.Address{{
				Addr: "jdoe@machine.example",
			}},
		},
		// RFC 5322, Appendix A.1.1
		{
			`John Doe <jdoe@machine.example>`,
			[]*email.Address{{
				Name: "John Doe",
				Addr: "jdoe@machine.example",
			}},
		},
		// RFC 5322, Appendix A.1.2
		{
			`"Joe Q. Public" <john.q.public@example.com>`,
			[]*email.Address{{
				Name: "Joe Q. Public",
				Addr: "john.q.public@example.com",
			}},
		},
		{
			`"John (middle) Doe" <jdoe@machine.example>`,
			[]*email.Address{{
				Name: "John (middle) Doe",
				Addr: "jdoe@machine.example",
			}},
		},
		{
			`John (middle) Doe <jdoe@machine.example>`,
			[]*email.Address{{
				Name: "John (middle) Doe",
				Addr: "jdoe@machine.example",
			}},
		},
		{
			`John !@M@! Doe <jdoe@machine.example>`,
			[]*email.Address{{
				Name: "John !@M@! Doe",
				Addr: "jdoe@machine.example",
			}},
		},
		{
			`"John <middle> Doe" <jdoe@machine.example>`,
			[]*email.Address{{
				Name: "John <middle> Doe",
				Addr: "jdoe@machine.example",
			}},
		},
		{
			`Mary Smith <mary@x.test>, jdoe@example.org, Who? <one@y.test>`,
			[]*email.Address{
				{
					Name: "Mary Smith",
					Addr: "mary@x.test",
				},
				{
					Addr: "jdoe@example.org",
				},
				{
					Name: "Who?",
					Addr: "one@y.test",
				},
			},
		},
		{
			`<boss@nil.test>, "Giant; \"Big\" Box" <sysservices@example.net>`,
			[]*email.Address{
				{
					Addr: "boss@nil.test",
				},
				{
					Name: `Giant; "Big" Box`,
					Addr: "sysservices@example.net",
				},
			},
		},
		// RFC 5322, Appendix A.6.1
		{
			`Joe Q. Public <john.q.public@example.com>`,
			[]*email.Address{{
				Name: "Joe Q. Public",
				Addr: "john.q.public@example.com",
			}},
		},
		// RFC 5322, Appendix A.1.3
		{
			`group1: groupaddr1@example.com;`,
			[]*email.Address{
				{
					Name: "",
					Addr: "groupaddr1@example.com",
				},
			},
		},
		{
			`empty group: ;`,
			[]*email.Address(nil),
		},
		{
			`A Group:Ed Jones <c@a.test>,joe@where.test,John <jdoe@one.test>;`,
			[]*email.Address{
				{
					Name: "Ed Jones",
					Addr: "c@a.test",
				},
				{
					Name: "",
					Addr: "joe@where.test",
				},
				{
					Name: "John",
					Addr: "jdoe@one.test",
				},
			},
		},
		{
			`Group1: <addr1@example.com>;, Group 2: addr2@example.com;, John <addr3@example.com>`,
			[]*email.Address{
				{
					Name: "",
					Addr: "addr1@example.com",
				},
				{
					Name: "",
					Addr: "addr2@example.com",
				},
				{
					Name: "John",
					Addr: "addr3@example.com",
				},
			},
		},
		// RFC 2047 "Q"-encoded ISO-8859-1 address.
		{
			`=?iso-8859-1?q?J=F6rg_Doe?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg Doe`,
					Addr: "joerg@example.com",
				},
			},
		},
		// RFC 2047 "Q"-encoded US-ASCII address. Dumb but legal.
		{
			`=?us-ascii?q?J=6Frg_Doe?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jorg Doe`,
					Addr: "joerg@example.com",
				},
			},
		},
		// RFC 2047 "Q"-encoded UTF-8 address.
		{
			`=?utf-8?q?J=C3=B6rg_Doe?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg Doe`,
					Addr: "joerg@example.com",
				},
			},
		},
		// RFC 2047 "Q"-encoded UTF-8 address with multiple encoded-words.
		{
			`=?utf-8?q?J=C3=B6rg?=  =?utf-8?q?Doe?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `JörgDoe`,
					Addr: "joerg@example.com",
				},
			},
		},
		// RFC 2047, Section 8.
		{
			`=?ISO-8859-1?Q?Andr=E9?= Pirard <PIRARD@vm1.ulg.ac.be>`,
			[]*email.Address{
				{
					Name: `André Pirard`,
					Addr: "PIRARD@vm1.ulg.ac.be",
				},
			},
		},
		// Custom example of RFC 2047 "B"-encoded ISO-8859-1 address.
		{
			`=?ISO-8859-1?B?SvZyZw==?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg`,
					Addr: "joerg@example.com",
				},
			},
		},
		// Custom example of RFC 2047 "B"-encoded UTF-8 address.
		{
			`=?UTF-8?B?SsO2cmc=?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg`,
					Addr: "joerg@example.com",
				},
			},
		},
		// Custom example with "." in name. For issue 4938
		{
			`Asem H. <noreply@example.com>`,
			[]*email.Address{
				{
					Name: `Asem H.`,
					Addr: "noreply@example.com",
				},
			},
		},
		// RFC 6532 3.2.3, qtext /= UTF8-non-ascii
		{
			`"Gø Pher" <gopher@example.com>`,
			[]*email.Address{
				{
					Name: `Gø Pher`,
					Addr: "gopher@example.com",
				},
			},
		},
		// RFC 6532 3.2, atext /= UTF8-non-ascii
		{
			`µ <micro@example.com>`,
			[]*email.Address{
				{
					Name: `µ`,
					Addr: "micro@example.com",
				},
			},
		},
		// RFC 6532 3.2.2, local address parts allow UTF-8
		{
			`Micro <µ@example.com>`,
			[]*email.Address{
				{
					Name: `Micro`,
					Addr: "µ@example.com",
				},
			},
		},
		// RFC 6532 3.2.4, domains parts allow UTF-8
		{
			`Micro <micro@µ.example.com>`,
			[]*email.Address{
				{
					Name: `Micro`,
					Addr: "micro@µ.example.com",
				},
			},
		},
		// Issue 14866
		{
			`"" <emptystring@example.com>`,
			[]*email.Address{
				{
					Name: "",
					Addr: "emptystring@example.com",
				},
			},
		},
		// CFWS
		{
			`<cfws@example.com> (CFWS (cfws))  (another comment)`,
			[]*email.Address{
				{
					Name: "",
					Addr: "cfws@example.com",
				},
			},
		},
		{
			`<cfws@example.com> ()  (another comment), <cfws2@example.com> (another)`,
			[]*email.Address{
				{
					Name: "",
					Addr: "cfws@example.com",
				},
				{
					Name: "",
					Addr: "cfws2@example.com",
				},
			},
		},
		// Comment as display name
		{
			`john@example.com (John Doe)`,
			[]*email.Address{
				{
					Name: "John Doe",
					Addr: "john@example.com",
				},
			},
		},
		// Comment and display name
		{
			`John Doe <john@example.com> (Joey)`,
			[]*email.Address{
				{
					Name: "John Doe",
					Addr: "john@example.com",
				},
			},
		},
		// Comment as display name, no space
		{
			`john@example.com(John Doe)`,
			[]*email.Address{
				{
					Name: "John Doe",
					Addr: "john@example.com",
				},
			},
		},
		// Comment as display name, Q-encoded
		{
			`asjo@example.com (Adam =?utf-8?Q?Sj=C3=B8gren?=)`,
			[]*email.Address{
				{
					Name: "Adam Sjøgren",
					Addr: "asjo@example.com",
				},
			},
		},
		// Comment as display name, Q-encoded and tab-separated
		{
			`asjo@example.com (Adam	=?utf-8?Q?Sj=C3=B8gren?=)`,
			[]*email.Address{
				{
					Name: "Adam Sjøgren",
					Addr: "asjo@example.com",
				},
			},
		},
		// Nested comment as display name, Q-encoded
		{
			`asjo@example.com (Adam =?utf-8?Q?Sj=C3=B8gren?= (Debian))`,
			[]*email.Address{
				{
					Name: "Adam Sjøgren (Debian)",
					Addr: "asjo@example.com",
				},
			},
		},
	}
	for _, test := range tests {
		if len(test.exp) == 1 {
			addr, err := ParseAddress(test.addrsStr)
			if err != nil {
				t.Errorf("Failed parsing (single) %q: %v", test.addrsStr, err)
				continue
			}
			if !reflect.DeepEqual([]*email.Address{addr}, test.exp) {
				t.Errorf("Parse (single) of %q: got %+v, want %+v", test.addrsStr, addr, test.exp)
			}
		}

		addrs, err := ParseAddressList(test.addrsStr)
		if err != nil {
			t.Errorf("Failed parsing (list) %q: %v", test.addrsStr, err)
			continue
		}
		if !reflect.DeepEqual(addrs, test.exp) {
			t.Errorf("Parse (list) of %q: got %+v, want %+v", test.addrsStr, addrs, test.exp)
		}
	}
}

func TestAddressParser(t *testing.T) {
	tests := []struct {
		addrsStr string
		exp      []*email.Address
	}{
		// Bare address
		{
			`jdoe@machine.example`,
			[]*email.Address{{
				Addr: "jdoe@machine.example",
			}},
		},
		// RFC 5322, Appendix A.1.1
		{
			`John Doe <jdoe@machine.example>`,
			[]*email.Address{{
				Name: "John Doe",
				Addr: "jdoe@machine.example",
			}},
		},
		// RFC 5322, Appendix A.1.2
		{
			`"Joe Q. Public" <john.q.public@example.com>`,
			[]*email.Address{{
				Name: "Joe Q. Public",
				Addr: "john.q.public@example.com",
			}},
		},
		{
			`Mary Smith <mary@x.test>, jdoe@example.org, Who? <one@y.test>`,
			[]*email.Address{
				{
					Name: "Mary Smith",
					Addr: "mary@x.test",
				},
				{
					Addr: "jdoe@example.org",
				},
				{
					Name: "Who?",
					Addr: "one@y.test",
				},
			},
		},
		{
			`<boss@nil.test>, "Giant; \"Big\" Box" <sysservices@example.net>`,
			[]*email.Address{
				{
					Addr: "boss@nil.test",
				},
				{
					Name: `Giant; "Big" Box`,
					Addr: "sysservices@example.net",
				},
			},
		},
		// RFC 2047 "Q"-encoded ISO-8859-1 address.
		{
			`=?iso-8859-1?q?J=F6rg_Doe?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg Doe`,
					Addr: "joerg@example.com",
				},
			},
		},
		// RFC 2047 "Q"-encoded US-ASCII address. Dumb but legal.
		{
			`=?us-ascii?q?J=6Frg_Doe?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jorg Doe`,
					Addr: "joerg@example.com",
				},
			},
		},
		// RFC 2047 "Q"-encoded ISO-8859-15 address.
		{
			`=?ISO-8859-15?Q?J=F6rg_Doe?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg Doe`,
					Addr: "joerg@example.com",
				},
			},
		},
		// RFC 2047 "B"-encoded windows-1252 address.
		{
			`=?windows-1252?q?Andr=E9?= Pirard <PIRARD@vm1.ulg.ac.be>`,
			[]*email.Address{
				{
					Name: `André Pirard`,
					Addr: "PIRARD@vm1.ulg.ac.be",
				},
			},
		},
		// Custom example of RFC 2047 "B"-encoded ISO-8859-15 address.
		{
			`=?ISO-8859-15?B?SvZyZw==?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg`,
					Addr: "joerg@example.com",
				},
			},
		},
		// Custom example of RFC 2047 "B"-encoded UTF-8 address.
		{
			`=?UTF-8?B?SsO2cmc=?= <joerg@example.com>`,
			[]*email.Address{
				{
					Name: `Jörg`,
					Addr: "joerg@example.com",
				},
			},
		},
		// Custom example with "." in name. For issue 4938
		{
			`Asem H. <noreply@example.com>`,
			[]*email.Address{
				{
					Name: `Asem H.`,
					Addr: "noreply@example.com",
				},
			},
		},
	}

	for _, test := range tests {
		if len(test.exp) == 1 {
			addr, err := ParseAddress(test.addrsStr)
			if err != nil {
				t.Errorf("Failed parsing (single) %q: %v", test.addrsStr, err)
				continue
			}
			if !reflect.DeepEqual([]*email.Address{addr}, test.exp) {
				t.Errorf("Parse (single) of %q: got %+v, want %+v", test.addrsStr, addr, test.exp)
			}
		}

		addrs, err := ParseAddressList(test.addrsStr)
		if err != nil {
			t.Errorf("Failed parsing (list) %q: %v", test.addrsStr, err)
			continue
		}
		if !reflect.DeepEqual(addrs, test.exp) {
			t.Errorf("Parse (list) of %q: got %+v, want %+v", test.addrsStr, addrs, test.exp)
		}
	}
}

func TestAddressString(t *testing.T) {
	tests := []struct {
		addr *email.Address
		exp  string
	}{
		{
			&email.Address{Addr: "bob@example.com"},
			"<bob@example.com>",
		},
		{ // quoted local parts: RFC 5322, 3.4.1. and 3.2.4.
			&email.Address{Addr: `my@idiot@address@example.com`},
			`<"my@idiot@address"@example.com>`,
		},
		{ // quoted local parts
			&email.Address{Addr: ` @example.com`},
			`<" "@example.com>`,
		},
		{
			&email.Address{Name: "Bob", Addr: "bob@example.com"},
			`"Bob" <bob@example.com>`,
		},
		{
			// note the ö (o with an umlaut)
			&email.Address{Name: "Böb", Addr: "bob@example.com"},
			`=?utf-8?q?B=C3=B6b?= <bob@example.com>`,
		},
		{
			&email.Address{Name: "Bob Jane", Addr: "bob@example.com"},
			`"Bob Jane" <bob@example.com>`,
		},
		{
			&email.Address{Name: "Böb Jacöb", Addr: "bob@example.com"},
			`=?utf-8?q?B=C3=B6b_Jac=C3=B6b?= <bob@example.com>`,
		},
		{ // https://golang.org/issue/12098
			&email.Address{Name: "Rob", Addr: ""},
			`"Rob" <@>`,
		},
		{ // https://golang.org/issue/12098
			&email.Address{Name: "Rob", Addr: "@"},
			`"Rob" <@>`,
		},
		{
			&email.Address{Name: "Böb, Jacöb", Addr: "bob@example.com"},
			`=?utf-8?b?QsO2YiwgSmFjw7Zi?= <bob@example.com>`,
		},
		{
			&email.Address{Name: "=??Q?x?=", Addr: "hello@world.com"},
			`"=??Q?x?=" <hello@world.com>`,
		},
		{
			&email.Address{Name: "=?hello", Addr: "hello@world.com"},
			`"=?hello" <hello@world.com>`,
		},
		{
			&email.Address{Name: "world?=", Addr: "hello@world.com"},
			`"world?=" <hello@world.com>`,
		},
		{
			// should q-encode even for invalid utf-8.
			&email.Address{Name: string([]byte{0xed, 0xa0, 0x80}), Addr: "invalid-utf8@example.net"},
			"=?utf-8?q?=ED=A0=80?= <invalid-utf8@example.net>",
		},
	}
	for _, test := range tests {
		s := FormatAddress(test.addr)
		if s != test.exp {
			t.Errorf("FormatAddress(%+v) = %v, want %v", *test.addr, s, test.exp)
			continue
		}

		// Check round-trip.
		if test.addr.Addr != "" && test.addr.Addr != "@" {
			a, err := ParseAddress(test.exp)
			if err != nil {
				t.Errorf("ParseAddress(%#q): %v", test.exp, err)
				continue
			}
			if a.Name != test.addr.Name || a.Addr != test.addr.Addr {
				t.Errorf("ParseAddress(%#q) = %#v, want %#v", test.exp, a, test.addr)
			}
		}
	}
}

// Check if all valid addresses can be parsed, formatted and parsed again
func TestAddressParsingAndFormatting(t *testing.T) {
	// Should pass
	tests := []string{
		`<Bob@example.com>`,
		`<bob.bob@example.com>`,
		`<".bob"@example.com>`,
		`<" "@example.com>`,
		`<some.mail-with-dash@example.com>`,
		`<"dot.and space"@example.com>`,
		`<"very.unusual.@.unusual.com"@example.com>`,
		`<admin@mailserver1>`,
		`<postmaster@localhost>`,
		"<#!$%&'*+-/=?^_`{}|~@example.org>",
		`<"very.(),:;<>[]\".VERY.\"very@\\ \"very\".unusual"@strange.example.com>`, // escaped quotes
		`<"()<>[]:,;@\\\"!#$%&'*+-/=?^_{}| ~.a"@example.org>`,                      // escaped backslashes
		`<"Abc\\@def"@example.com>`,
		`<"Joe\\Blow"@example.com>`,
		`<test1/test2=test3@example.com>`,
		`<def!xyz%abc@example.com>`,
		`<_somename@example.com>`,
		`<joe@uk>`,
		`<~@example.com>`,
		`<"..."@test.com>`,
		`<"john..doe"@example.com>`,
		`<"john.doe."@example.com>`,
		`<".john.doe"@example.com>`,
		`<"."@example.com>`,
		`<".."@example.com>`,
		`<"0:"@0>`,
	}

	for _, test := range tests {
		addr, err := ParseAddress(test)
		if err != nil {
			t.Errorf("Couldn't parse address %s: %s", test, err.Error())
			continue
		}
		str := FormatAddress(addr)
		addr, err = ParseAddress(str)
		if err != nil {
			t.Errorf("ParseAddr(%q) error: %v", test, err)
			continue
		}

		if got := FormatAddress(addr); got != test {
			t.Errorf("String() round-trip = %q; want %q", got, test)
			continue
		}

	}

	// Should fail
	badTests := []string{
		`<Abc.example.com>`,
		`<A@b@c@example.com>`,
		`<a"b(c)d,e:f;g<h>i[j\k]l@example.com>`,
		`<just"not"right@example.com>`,
		`<this is"not\allowed@example.com>`,
		`<this\ still\"not\\allowed@example.com>`,
		`<john..doe@example.com>`,
		`<john.doe@example..com>`,
		`<john.doe@example..com>`,
		`<john.doe.@example.com>`,
		`<john.doe.@.example.com>`,
		`<.john.doe@example.com>`,
		`<@example.com>`,
		`<.@example.com>`,
		`<test@.>`,
		`< @example.com>`,
		`<""test""blah""@example.com>`,
		`<""@0>`,
	}

	for _, test := range badTests {
		_, err := ParseAddress(test)
		if err == nil {
			t.Errorf("Should have failed to parse address: %s", test)
			continue
		}

	}

}

func TestAddressFormattingAndParsing(t *testing.T) {
	tests := []*email.Address{
		{Name: "@lïce", Addr: "alice@example.com"},
		{Name: "Böb O'Connor", Addr: "bob@example.com"},
		{Name: "???", Addr: "bob@example.com"},
		{Name: "Böb ???", Addr: "bob@example.com"},
		{Name: "Böb (Jacöb)", Addr: "bob@example.com"},
		{Name: "à#$%&'(),.:;<>@[]^`{|}~'", Addr: "bob@example.com"},
		// https://golang.org/issue/11292
		{Name: "\"\\\x1f,\"", Addr: "0@0"},
		// https://golang.org/issue/12782
		{Name: "naé, mée", Addr: "test.mail@gmail.com"},
	}

	for i, test := range tests {
		str := FormatAddress(test)
		parsed, err := ParseAddress(str)
		if err != nil {
			t.Errorf("test #%d: ParseAddr(%q) error: %v", i, str, err)
			continue
		}
		if parsed.Name != test.Name {
			t.Errorf("test #%d: Parsed name = %q; want %q", i, parsed.Name, test.Name)
		}
		if parsed.Addr != test.Addr {
			t.Errorf("test #%d: Parsed address = %q; want %q", i, parsed.Addr, test.Addr)
		}
	}
}

func TestReferencesParsing(t *testing.T) {
	tests := []struct {
		refs string
		want []string
	}{
		0: {
			`<jdoe@machine.example>`,
			[]string{`<jdoe@machine.example>`},
		},
		1: {
			`<mary@x.test> <jdoe@example.org> <one@y.test>`,
			[]string{`<mary@x.test>`, `<jdoe@example.org>`, `<one@y.test>`},
		},
		2: {
			`<boss@nil.test> <"Giant; \"Big\" Box"@example.net>`,
			[]string{`<boss@nil.test>`, `<"Giant; \"Big\" Box"@example.net>`},
		},
	}
	for i, test := range tests {
		got, err := ParseReferences(test.refs)
		if err != nil {
			t.Errorf("%d: error: %v", i, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%d: refs=%v, want %v", i, got, test.want)
		}
	}
}
