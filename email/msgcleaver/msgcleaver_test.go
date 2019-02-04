package msgcleaver

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"mime"
	"strings"
	"testing"

	"spilled.ink/third_party/imf"

	"spilled.ink/email/msgbuilder"

	"crawshaw.io/iox"
)

func TestCleaveQuotedPrintable(t *testing.T) {
	filer := iox.NewFiler(0)
	defer filer.Shutdown(context.Background())

	r := strings.NewReader(strings.Replace(textQuotedPrintable, "\n", "\r\n", -1))
	msg, err := Cleave(filer, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d parts: %v", len(msg.Parts), msg.Parts)
	}
	part := msg.Parts[0]
	if got, want := part.ContentType, "text/plain"; got != want {
		t.Errorf("ContentType=%s, want %s", got, want)
	}
	b, err := ioutil.ReadAll(part.Content)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(`Hello,

You have received this message because you are a contact of the domain pkgfort.com with the username "foo".
`, "\n", "\r\n", -1)
	if got := string(b); got != want {
		t.Errorf("unexpected quoted-printable content: %q", got)
	}
}

const textQuotedPrintable = `To: david@zentus.com
Subject: [Gandi] pkgfort.com expired yesterday
From: "Gandi" <support-renew@gandi.net>
Date: Fri, 13 Jul 2018 16:39:01 -0000
MIME-Version: 1.0
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: quoted-printable
Message-Id: <20180713163903.9B84B41ED4@mailer.gandi.net>

Hello,

You have received this message because you are a contact of the domain pkgf=
ort.com with the username "foo".
`

func TestUpperQuotedPrintable(t *testing.T) {
	filer := iox.NewFiler(0)
	defer filer.Shutdown(context.Background())

	r := strings.NewReader(strings.Replace(textUpperQuotedPrintable, "\n", "\r\n", -1))
	msg, err := Cleave(filer, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d parts: %v", len(msg.Parts), msg.Parts)
	}
	part := msg.Parts[0]
	if got, want := part.ContentType, "text/plain"; got != want {
		t.Errorf("ContentType=%s, want %s", got, want)
	}
	b, err := ioutil.ReadAll(part.Content)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(`Hello,

You have received this message because you are a contact of the domain pkgfort.com with the username "foo".
`, "\n", "\r\n", -1)
	if got := string(b); got != want {
		t.Errorf("unexpected quoted-printable content: %q", got)
	}
}

const textUpperQuotedPrintable = `MIME-Version: 1.0
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: QUOTED-PRINTABLE

Hello,

You have received this message because you are a contact of the domain pkgf=
ort.com with the username "foo".
`

func TestCleaveMultipartAlt(t *testing.T) {
	filer := iox.NewFiler(0)
	defer filer.Shutdown(context.Background())

	r := strings.NewReader(strings.Replace(textMultipartAlt, "\n", "\r\n", -1))
	msg, err := Cleave(filer, r)
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()
	if len(msg.Parts) != 3 {
		t.Fatalf("expected 3 part, got %d parts: %v", len(msg.Parts), msg.Parts)
	}

	plainText := msg.Parts[0]
	if got, want := plainText.ContentType, "text/plain"; got != want {
		t.Errorf("msg.Parts[0].ContentType=%s, want %s", got, want)
	}
	if got, want := plainText.ContentTransferEncoding, "7bit"; got != want {
		t.Errorf("msg.Parts[0].ContentTransferEncoding=%s, want %s", got, want)
	}
	// TODO: remove?
	//if got, want := plainText.Path, "1.1"; got != want {
	//	t.Errorf("msg.Parts[0].Path=%q, want %q", got, want)
	//}

	htmlText := msg.Parts[1]
	if got, want := htmlText.ContentType, "text/html"; got != want {
		t.Errorf("msg.Parts[1].ContentType=%s, want %s", got, want)
	}
	if got, want := htmlText.ContentTransferEncoding, "quoted-printable"; got != want {
		t.Errorf("msg.Parts[1].ContentTransferEncoding=%s, want %q", got, want)
	}
	if got, want := htmlText.ContentTransferSize, int64(43); got != want {
		t.Errorf("msg.Parts[1].ContentTransferSize=%d, want %d", got, want)
	}
	if got, want := htmlText.Content.Size(), int64(31); got != want {
		t.Errorf("msg.Parts[1].Content.Size()=%d, want %d", got, want)
	}
	// TODO: remove?
	//if got, want := htmlText.Path, "1.2"; got != want {
	//	t.Errorf("msg.Parts[1].Path=%q, want %q", got, want)
	//}

	htmlContent, err := ioutil.ReadAll(htmlText.Content)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(htmlContent), "<b>Rich</b> text. Hello, 世界"; got != want {
		t.Errorf("msg.Parts[1].Content=%q, want %q", got, want)
	}

	richText := msg.Parts[2]
	if got, want := richText.ContentType, "text/rich"; got != want {
		t.Errorf("msg.Parts[2].ContentType=%s, want %s", got, want)
	}
	if !richText.IsCompressed {
		t.Error("msg.Parts[2] not compressed")
	}
	if got, want := int(richText.CompressedSize), 198; got != want {
		t.Errorf("msg.Parts[2].CompressedSize=%d, want %d", got, want)
	}
	const contentLen = (1<<13)*(6+2) + // repeated section with CRLF
		63 // rest of the message
	if got, want := int(richText.Content.Size()), contentLen; got != want {
		t.Errorf("msg.Parts[2].Content.Size()=%d, want %d", got, want)
	}
	// TODO: remove?
	//if got, want := richText.Path, "1.3"; got != want {
	//	t.Errorf("msg.Parts[2].Path=%q, want %q", got, want)
	//}
}

// This is busted, incorrect MIME input.
// The cleaver will clean it up and report sensible encoding sizes.
var textMultipartAlt = `MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b2"

--b2
Content-Type: text/plain; charset="utf-8"

Plain text.
--b2
Content-Type: text/html; charset="utf-8"

<b>Rich</b> text. Hello, 世界
--b2
Content-Type: text/rich; charset="utf-8"

*Rich* text. Will get compressed because there's a lot of it.
` + strings.Repeat("repeat\n", 1<<13) + `
--b2--
`

func TestCleaveRelatedAndAttached(t *testing.T) {
	filer := iox.NewFiler(0)
	defer filer.Shutdown(context.Background())

	r := strings.NewReader(strings.Replace(relatedAndAttached, "\n", "\r\n", -1))
	msg, err := Cleave(filer, r)
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()
	if len(msg.Parts) != 6 {
		t.Fatalf("expected 3 part, got %d parts: %v", len(msg.Parts), msg.Parts)
	}
	_, params, err := mime.ParseMediaType(string(msg.Headers.Get("Content-Type")))
	if err != nil {
		t.Fatal(err)
	}
	if b := params["boundary"]; b == "" || b == ".6Cq99EotC3X7GA2v." {
		t.Errorf("invalid top-level boundary %q, expect cleaver to create a new one", b)
	}
	if msg.Seed == 0 {
		t.Error("Seed=0, want non-zero")
	}

	var buf1, buf2 bytes.Buffer
	builder := msgbuilder.Builder{Filer: filer}
	if err := builder.Build(&buf1, msg); err != nil {
		t.Errorf("cleaved message could not be rebuilt: %v", err)
	}
	if err := builder.Build(&buf2, msg); err != nil {
		t.Errorf("cleaved message could not be rebuilt: %v", err)
	}
	if buf1.String() != buf2.String() {
		t.Error("subsequent rebuilds result in different messages")
		t.Logf("rebuild1:\n%s", buf1.String())
		t.Logf("rebuild2:\n%s", buf2.String())
	}

	if got := buf1.String(); !strings.Contains(got, params["boundary"]) {
		t.Errorf("build message does not contain the multipart boundary %q from the cleaved header:\n%s", params["boundary"], got)
	}
}

// TODO: de-duplicate with msgbuilder_test.go
const relatedAndAttached = `MIME-Version: 1.0
Content-Type: multipart/mixed; boundary=.6Cq99EotC3X7GA2v.

--.6Cq99EotC3X7GA2v.
Content-Type: multipart/alternative; boundary=".AZT9wvov/MBB0/8S."

--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/plain; charset="UTF-8"

Hello, World!
--.AZT9wvov/MBB0/8S.
Content-Type: multipart/related; boundary=".BFtzyG5P+V/2YqXu."

--.BFtzyG5P+V/2YqXu.
Content-Disposition: inline
Content-Type: text/html; charset="UTF-8"

<img src="cid:v1@mycid /> <img src="cid:v2@midcid" />
--.BFtzyG5P+V/2YqXu.
Content-Disposition: inline; filename="v1@mycid"
Content-Id: <v1@mycid>
Content-Type: image/svg+xml

<svg height="10" width="10"></svg>
--.BFtzyG5P+V/2YqXu.
Content-Disposition: inline; filename="v2@mycid"
Content-Id: <v2@mycid>
Content-Type: image/svg+xml

<svg height="20" width="20"></svg>
--.BFtzyG5P+V/2YqXu.--

--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/watch-html

<b>Secret</b> apple watch message!
--.AZT9wvov/MBB0/8S.--

--.6Cq99EotC3X7GA2v.
Content-Disposition: attachment; filename="invoice.pdf"
Content-Transfer-Encoding: base64
Content-Type: application/pdf; name="invoice.pdf"

UERGAA==
--.6Cq99EotC3X7GA2v.--
`

func TestLongHeaders(t *testing.T) {
	filer := iox.NewFiler(0)
	defer filer.Shutdown(context.Background())

	r := strings.NewReader(strings.Replace(longHeaders, "\n", "\r\n", -1))
	msg, err := Cleave(filer, r)
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	//t.Errorf("msg.Headers: %s", msg.Headers)
	//t.Errorf("msg.Headers: From: %s", msg.Headers.Get("From"))
	addr1, err := imf.ParseAddress(string(msg.Headers.Get("From")))
	if err != nil {
		t.Fatal(err)
	}
	if want, got := longFromAddr, addr1.Addr; want != got {
		t.Errorf("first header parse From=%s, want %s", got, want)
	}

	buf := new(bytes.Buffer)
	if _, err := msg.Headers.Encode(buf); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(buf, msg.Parts[0].Content); err != nil {
		t.Fatal(err)
	}

	msg2, err := Cleave(filer, buf)
	if err != nil {
		t.Fatal(err)
	}
	defer msg2.Close()

	t.Logf("re-encoded From: %s", msg2.Headers.Get("From"))
	addr2, err := imf.ParseAddress(string(msg2.Headers.Get("From")))
	if err != nil {
		t.Fatal(err)
	}
	if want, got := longFromAddr, addr2.Addr; want != got {
		t.Errorf("second header parse From=%s, want %s", got, want)
	}
}

const longFromAddr = `reply+ZXlKMGVYQWlPaUpLVjFRaUxDSmhiR2NpT2lKU1V6VXhNaUo5LmV5SmtZWFJoSWpwN0ltbGtJam8xTmpjeU15d2lkSGx3WlNJNkltWmxaV1JpWVdOcklpd2lkWE5sY2w5cFpDSTZPREkwTjMwc0ltVjRjQ0k2TVRnMk16VTNORFUxT1gwLmFfYVN0aC1aQW9Ud0x0M0w3OXphN3JQeXQ1M05wSXhwUnJCMWRWV1VCS0gzSGNMVkFtQXJsbUVUbjBSOGp3UGN4clF6UmNXbGFTWkxOaHdvRXpSbTZ1dWhUZW9XX0xPR3hjSGl0Xzc1NDQ3WWZFamt5c25FM3NBalBSMEVWbG9qNWFxQTJSR1BmbVFlY1EyRFBPUktncEFtYU13TjFsczRZOWpNekZKTWllSmVxVW5lbGE1d1FERnhVLVh4NG5aanJxSWZwM1VsUUJHWkFFcDY3bHJnRUtvTlM4ZmRmVk1yanlURFp0UHlXS1gwOHZIemV4NDFPaWZTbUZ1d3Q4Ukhsd016ZWpxOXJaRG5FSmtaSU1Cdi1KVFlYRnZsRVlGQVRIdldOU1Fqbk1aUW1MZVk2VVM2Mm1ySmlXWHhDeGJGU1dXVFZuMHNOYnRpa0xpT1QtLWdnQQ==@automatedsystem.com`

const longHeaders = `MIME-Version: 1.0
From: An Automated System <` + longFromAddr + `>
Content-Type: text/plain

Hello!
`
