package htmlsafe

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
)

var sanitizeTests = []struct {
	name string
	in   string
	out  string
	err  error
	opts *Options
	rurl func(attr string, url *url.URL) string
}{
	{
		name: "banal-strict",
		in:   "<html><head><title>hello</title></head><body><p>hello<br/>world</p></body></html>",
		out:  "<head></head><p>hello<br/>world</p>",
		opts: StrictEmail,
	},
	{
		name: "banal-safe",
		in:   "<html><head><title>hello</title></head><body><p>hello<br/>world</p></body></html>",
		out:  "<html><head><title>hello</title></head><body><p>hello<br/>world</p></body></html>",
		opts: Safe,
	},
	{
		name: "script",
		in:   `<p>one <script>alert("two");</script> three</p>`,
		out:  `<p>one  three</p>`,
	},
	{
		name: "script-in-script",
		in:   `<p>one <script><!--<script>alert("two");</script>--></script> three</p>`,
		out:  `<p>one  three</p>`,
	},
	{
		name: "script-in-pre",
		in:   `<p>one<pre><script>alert("two");</script></pre>three</p>`,
		out:  `<p>onethree</p>`,
	},
	{
		name: "filter styles",
		in:   `<div style="border: 1px solid black; -webkit-dangerous: foo; font: sans-serif"></div>`,
		out:  `<div style="border: 1px solid black; font: sans-serif;"></div>`,
	},
	{
		name: "style quoting",
		in:   `<div style='font-family: "my amazing font"'></div>`,
		out:  `<div style="font-family: &#34;my amazing font&#34;;"></div>`,
	},
	{
		name: "tag in attr", // TODO: should we escape this? &lt;
		in:   `<div id="<script>alert(1)</script>"/>`,
		out:  `<div id="<script>alert(1)</script>"/>`,
	},
	{
		name: "remove attr",
		in:   `<body onload="javascript:alert(1);">hi</body>`,
		out:  `<body>hi</body>`,
	},
	{
		name: "remove js links",
		in:   `<a href="  javascript:alert(1);  ">1</a><a href="alert(2);">2</a><a href="/foo">3</a>`,
		out:  `<a>1</a><a>2</a><a>3</a>`,
	},
	{
		name: "keep external links by default",
		in:   `<a href="https://example.com">ex</a><img src="https://example.com/foo.gif"/>`,
		out:  `<a href="https://example.com">ex</a><img src="https://example.com/foo.gif"/>`,
	},
	{
		name: "remove bad src",
		in:   `<img src="javascript:alert(1);" /><img src="/foobar" />`,
		out:  `<img/><img/>`,
	},
	{
		name: "keep cid src",
		in:   `<img src="cid:foobar"/>`,
		out:  `<img src="cid:foobar"/>`,
	},
	{
		name: "escape link attrs",
		in:   `<img src='https://example.com/"foo"'/>`,
		out:  `<img src="https://example.com/%22foo%22"/>`,
	},
	{
		name: "replace URLs",
		in:   `<a href="http://bogus.com/foo" style='background:url("http://sketch.io/bar"), blue;'><img src="https://bad.com/baz"/></a>`,
		out:  `<a href="https://example.com/foo" style="background: url(&#34;https://example.com/bar&#34;), blue;"><img src="https://example.com/baz"/></a>`,
		rurl: func(attr string, url *url.URL) string {
			return "https://example.com" + url.Path
		},
	},
}

func TestSanitize(t *testing.T) {
	for _, test := range sanitizeTests {
		t.Run(test.name, func(t *testing.T) {
			s := Sanitizer{
				RewriteURL: test.rurl,
				Options:    test.opts,
			}
			src := strings.NewReader(test.in)
			dst := new(bytes.Buffer)

			n, err := s.Sanitize(dst, src)
			if err != test.err {
				t.Errorf("got err %v, want %v", err, test.err)
			}
			if n != dst.Len() {
				t.Errorf("n=%d want %d", n, dst.Len())
			}
			if got := dst.String(); got != test.out {
				t.Errorf("Sanitize(%q)\n\t   = %q,\n\twant %q", test.in, got, test.out)
			}
		})
	}
}

func BenchmarkSanitize(b *testing.B) {
	for _, test := range sanitizeTests {
		b.Run(test.name, func(b *testing.B) {
			s := Sanitizer{Options: test.opts}
			src := strings.NewReader(test.in)
			dst := new(bytes.Buffer)
			dst.Grow(len(test.in))

			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				src.Reset(test.in)
				dst.Truncate(0)

				if _, err := s.Sanitize(dst, src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
