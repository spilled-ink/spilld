package utf7mod

import "testing"

var tests = []struct {
	dec, enc string
	errstr   string
}{
	{dec: "&", enc: "&-"},
	{dec: "&&", enc: "&-&-"},
	{dec: "Hello, 世界", enc: "Hello, &ThZ1TA-"},
	{dec: "🤓", enc: "&2D7dEw-"},
	{dec: "~peter/mail/台北/日本語", enc: "~peter/mail/&U,BTFw-/&ZeVnLIqe-"},
}

func TestAppendEncode(t *testing.T) {
	for _, test := range tests {
		t.Run(test.dec, func(t *testing.T) {
			enc, err := AppendEncode(nil, []byte(test.dec))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(enc); got != test.enc {
				t.Errorf("encode %q=%q, want %q", test.dec, got, test.enc)
			}
		})
	}
}

func TestAppendDecode(t *testing.T) {
	for _, test := range tests {
		t.Run(test.dec, func(t *testing.T) {
			dec, err := AppendDecode(nil, []byte(test.enc))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(dec); got != test.dec {
				t.Errorf("encode %q=%q, want %q", test.enc, got, test.dec)
			}
		})
	}
}

func BenchmarkEncodeAlloc(b *testing.B) {
	dst := make([]byte, 0, 1024)

	var inputs [][]byte
	for _, test := range tests {
		if test.errstr != "" {
			continue
		}
		inputs = append(inputs, []byte(test.dec))
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			_, err := AppendEncode(dst, input)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkDecodeAlloc(b *testing.B) {
	dst := make([]byte, 0, 1024)

	var inputs [][]byte
	for _, test := range tests {
		if test.errstr != "" {
			continue
		}
		inputs = append(inputs, []byte(test.enc))
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			_, err := AppendDecode(dst, input)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
