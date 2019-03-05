package msgbuilder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"mime"
	"sort"
	"strings"
	"testing"

	"crawshaw.io/iox"
	"spilled.ink/email"
	"spilled.ink/email/dkim"
	"spilled.ink/third_party/imf"
)

func newBuilder(t *testing.T, boundary string) (b *Builder, cleanup func()) {
	b = &Builder{
		Filer: iox.NewFiler(0),
	}
	cleanup = func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b.Filer.Shutdown(ctx)
	}
	return b, cleanup
}

type stringReader struct {
	*strings.Reader
	closed bool
}

func (s *stringReader) Write([]byte) (int, error) { panic("Write not supported") }

func (s *stringReader) Close() error {
	s.closed = true
	return nil
}

func (s *stringReader) Len() int64 {
	return s.Size()
}

func strReader(s string) email.Buffer {
	s = strings.Replace(s, "\n", "\r\n", -1)
	return &stringReader{Reader: strings.NewReader(s)}
}

type buildTest struct {
	name     string
	header   map[string]string
	parts    []email.Part
	boundary string // if empty, generate predictable sequence
	want     string // all \n are converted into \r\n
}

var buildTests = []buildTest{
	{
		name: "plain-text-7bit",
		header: map[string]string{
			"To": "david@spilled.ink",
		},
		parts: []email.Part{{
			Content:     strReader("Hello, World!"),
			ContentType: "text/plain",
			IsBody:      true,
		}},
		want: `To: david@spilled.ink
MIME-Version: 1.0
Content-Disposition: inline
Content-Type: text/plain; charset="UTF-8"

Hello, World!`,
	},
	{
		name:   "plain-text-unicode",
		header: map[string]string{},
		parts: []email.Part{{
			Content:     strReader("Hello, 世界"),
			ContentType: "text/plain",
			IsBody:      true,
		}},
		want: `MIME-Version: 1.0
Content-Disposition: inline
Content-Transfer-Encoding: quoted-printable
Content-Type: text/plain; charset="UTF-8"

Hello, =E4=B8=96=E7=95=8C`,
	},
	{
		name:   "base64-folding",
		header: map[string]string{},
		parts: []email.Part{{
			Content: strReader(`Hello, 世界.
				RFC 2045, section 6.8 covers base64-encoding.
				In particular it calls out that: "The encoded
				output stream must be represented in lines of
				no more than 76 characters each.  All line
				breaks or other characters not found in Table
				1 must be ignored by decoding software."`),
			ContentType: "text/plain",
			IsBody:      true,
		}},
		want: `MIME-Version: 1.0
Content-Disposition: inline
Content-Transfer-Encoding: quoted-printable
Content-Type: text/plain; charset="UTF-8"

Hello, =E4=B8=96=E7=95=8C.
				RFC 2045, section 6.8 covers base64-encoding.
				In particular it calls out that: "The encoded
				output stream must be represented in lines of
				no more than 76 characters each.  All line
				breaks or other characters not found in Table
				1 must be ignored by decoding software."`,
	},
	{
		name:   "plain-and-html-unicode",
		header: map[string]string{},
		parts: []email.Part{
			{
				Content:     strReader("Hello, World!"),
				ContentType: "text/plain",
				IsBody:      true,
			},
			{
				Content:     strReader("<div>Hello, <b>World!</b></div>"),
				ContentType: "text/html",
				IsBody:      true,
			},
		},
		want: `MIME-Version: 1.0
Content-Type: multipart/alternative; boundary=".AZT9wvov/MBB0/8S."

--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/plain; charset="UTF-8"

Hello, World!
--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/html; charset="UTF-8"

<div>Hello, <b>World!</b></div>
--.AZT9wvov/MBB0/8S.--
`,
	},
	{
		name:   "long-html",
		header: map[string]string{},
		parts: []email.Part{{
			Content: strReader("<div>Hello, <b>World!</b> When faced with an " +
				"an extremely long line we switch encoding to make sure we " +
				"don't go anywhere near the 1000 character limit that the " +
				"RFCs traditionally demand of SMTP servers and some still " +
				"follow.</div>"),
			ContentType: "text/html",
			IsBody:      true,
		}},
		want: `MIME-Version: 1.0
Content-Disposition: inline
Content-Transfer-Encoding: quoted-printable
Content-Type: text/html; charset="UTF-8"

<div>Hello, <b>World!</b> When faced with an an extremely long line we swit=
ch encoding to make sure we don't go anywhere near the 1000 character limit=
 that the RFCs traditionally demand of SMTP servers and some still follow.<=
/div>`,
	},
	{
		name:   "attachments",
		header: map[string]string{},
		parts: []email.Part{
			{
				Content:     strReader("Hello, World!"),
				ContentType: "text/plain",
				IsBody:      true,
			},
			{
				Content:     strReader("<div>Hello, <b>World!</b></div>"),
				ContentType: "text/html",
				IsBody:      true,
			},
			{
				Content:      strReader("PDF\u0000"),
				ContentType:  "application/pdf",
				IsAttachment: true,
				Name:         "invoice.pdf",
			},
		},
		want: `MIME-Version: 1.0
Content-Type: multipart/mixed; boundary=".BFtzyG5P+V/2YqXu."

--.BFtzyG5P+V/2YqXu.
Content-Type: multipart/alternative; boundary=".AZT9wvov/MBB0/8S."

--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/plain; charset="UTF-8"

Hello, World!
--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/html; charset="UTF-8"

<div>Hello, <b>World!</b></div>
--.AZT9wvov/MBB0/8S.--

--.BFtzyG5P+V/2YqXu.
Content-Disposition: attachment; filename="invoice.pdf"
Content-Transfer-Encoding: base64
Content-Type: application/pdf; name="invoice.pdf"

UERGAA==
--.BFtzyG5P+V/2YqXu.--
`,
	},
	{
		name:   "related and attached",
		header: map[string]string{},
		parts: []email.Part{
			{
				Content:     strReader("Hello, World!"),
				ContentType: "text/plain",
				IsBody:      true,
			},
			{
				Content:     strReader(`<img src="cid:v1@mycid /> <img src="cid:v2@midcid" />`),
				ContentType: "text/html",
				IsBody:      true,
			},
			{
				Content:     strReader(`<b>Secret</b> apple watch message!`),
				ContentType: "text/watch-html",
				IsBody:      true,
			},
			{
				Content:     strReader(`<svg height="10" width="10"></svg>`),
				ContentType: "image/svg+xml",
				ContentID:   "v1@mycid",
				Name:        "img1.svg",
			},
			{
				Content:     strReader(`<svg height="20" width="20"></svg>`),
				ContentType: "image/svg+xml",
				ContentID:   "v2@mycid",
				Name:        "img2.svg",
			},
			{
				Content:      strReader("PDF\u0000"),
				ContentType:  "application/pdf",
				Name:         "invoice.pdf",
				IsAttachment: true,
			},
		},
		want: `MIME-Version: 1.0
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
Content-Disposition: inline; filename="img1.svg"
Content-Id: <v1@mycid>
Content-Type: image/svg+xml; name="img1.svg"

<svg height="10" width="10"></svg>
--.BFtzyG5P+V/2YqXu.
Content-Disposition: inline; filename="img2.svg"
Content-Id: <v2@mycid>
Content-Type: image/svg+xml; name="img2.svg"

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
`,
	},
	{
		name:   "attachment with contentid",
		header: map[string]string{},
		parts: []email.Part{
			{
				Content:     strReader("Hello, World!"),
				ContentType: "text/plain",
				IsBody:      true,
			},
			{
				Content:     strReader(`<b>Hello</b>, World!`),
				ContentType: "text/html",
				IsBody:      true,
			},
			{
				Content:      strReader("PDF\u0000"),
				ContentType:  "application/pdf",
				ContentID:    "foobarbaz",
				Name:         "invoice.pdf",
				IsAttachment: true,
			},
		},
		want: `MIME-Version: 1.0
Content-Type: multipart/mixed; boundary=".BFtzyG5P+V/2YqXu."

--.BFtzyG5P+V/2YqXu.
Content-Type: multipart/alternative; boundary=".AZT9wvov/MBB0/8S."

--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/plain; charset="UTF-8"

Hello, World!
--.AZT9wvov/MBB0/8S.
Content-Disposition: inline
Content-Type: text/html; charset="UTF-8"

<b>Hello</b>, World!
--.AZT9wvov/MBB0/8S.--

--.BFtzyG5P+V/2YqXu.
Content-Disposition: attachment; filename="invoice.pdf"
Content-Id: <foobarbaz>
Content-Transfer-Encoding: base64
Content-Type: application/pdf; name="invoice.pdf"

UERGAA==
--.BFtzyG5P+V/2YqXu.--
`,
	},
}

func TestBuild(t *testing.T) {
	for _, test := range buildTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			b, cleanup := newBuilder(t, test.boundary)
			defer cleanup()

			var keys []string
			for k := range test.header {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			hdr := new(email.Header)
			for _, k := range keys {
				hdr.Add(email.Key(k), []byte(test.header[k]))
			}

			msg := &email.Msg{
				MsgID:   0, // predictable boundary
				Headers: *hdr,
				Parts:   test.parts,
			}
			buf := b.Filer.BufferFile(0)
			if err := b.Build(buf, msg); err != nil {
				t.Fatal(err)
			}
			if _, err := buf.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			gotBytes, err := ioutil.ReadAll(buf)
			if err != nil {
				t.Fatal(err)
			}
			got := string(gotBytes)
			want := strings.Replace(test.want, "\n", "\r\n", -1)

			if got != want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
			}

			r := bufio.NewReader(strings.NewReader(got))
			gotHdr, err := imf.NewReader(r).ReadMIMEHeader()
			if err != nil {
				t.Fatal(err)
			}
			count, err := walkMimeRec(gotHdr, r)
			if err != nil {
				t.Fatal(err)
			}
			if count != len(test.parts) {
				t.Errorf("got %d parts, want %d", count, len(test.parts))
			}
		})
	}
}

func walkMimeRec(hdr email.Header, r io.Reader) (int, error) {
	mediaType, params, err := mime.ParseMediaType(string(hdr.Get("Content-Type")))
	if err != nil {
		return 1, err
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := imf.NewMultipartReader(r, params["boundary"])
		count := 0
		for i := 0; ; i++ {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return 0, fmt.Errorf("walkMime: corrupt mime part: %v", err)
			}
			n, err := walkMimeRec(part.Header, part)
			count += n
			if err != nil {
				return count, err
			}
		}
		return count, nil
	} else {
		return 1, nil
	}
}

func TestRandBoundary(t *testing.T) {
	rnd := rand.New(rand.NewSource(0))
	b1 := randBoundary(rnd)
	b2 := randBoundary(rnd)
	if b1 == b2 {
		t.Errorf("subsequent random boundaries are equal: %q", b1)
	}
}

func TestNoBody(t *testing.T) {
	b, cleanup := newBuilder(t, "")
	defer cleanup()

	err := b.Build(ioutil.Discard, &email.Msg{Parts: []email.Part{{
		Content:     strReader("hi"),
		Name:        "a-named-part-and-thus-not-body.txt",
		ContentType: "text/plain",
	}}})
	if err == nil {
		t.Errorf("expected missing body error")
	}
}

func TestDKIM(t *testing.T) {
	const testPrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIICXQIBAAKBgQDlPKmFqjWCqh4kZqdAoQmOWD695FTqiuGNEXtADNOt2PlmRjbi
LOwPJWdzTAjbABPddmPHJXDPLolEDPKbeOAdsBogvpw6ZKvGNd5ZcXYNyX7j2oyG
+RO5TbBSYWLfB1QgJWXztfUrPxWkd50CD6Ht11KA6h31coW2JYcbtRMbpwIDAQAB
AoGBAL5bz5I1s9XbmsgzjnP2xk60LPXXZESYK5DPkX+wpx9YbFJnwC+1ihlRwERY
QYpK2DQxmc3H45PIWyhtcBF3IPMz54lMa//IuzsmGz1XgelzEFJY9FbeedCUZvT1
PvOv+fMDg7otT8ueBkfAg2jG+G2ZOm0WQHdMV5iiWY8uFjrRAkEA9b2uf/IW6y/c
HPslOUY4nXOTTG0gfoMmtxuy3ZC3FXemLmXfS+4ueSiPasn8PYz8hnEKfs6mr6kq
9tJCB7A+8wJBAO7OmMetEEAqfTZtOxMJz4XOfrbKP+vOHVEkgIYuyEyQqZS/3zKm
9LrtvejrBpmGXyo2wO+6m4kmG/1yCYS35X0CQAJ1s5l0QuZ3xCxGF0lLeqWY0pCh
RwH9LhYHIPM2z55XZEJyopmP+McdsNHQ08WJ870kxIYga2q2tsdhs2eATCECQQDq
3UeHQl80LFWfXMh3zfNKjy8yiTFasglFT5gT4BjgrHoMMLTMdUVGPyHC3LtN7MjV
lKomXCoyNcfbePeBjvdlAkB2v5ZdS2oIYGrQ2I0pyPXRiXOVWlFreWh+v69mUcDq
pSFcE/MM8J5jjad3nN3cUaVjlbM36/3lKLRwVK024R2C
-----END RSA PRIVATE KEY-----
`

	s, err := dkim.NewSigner([]byte(testPrivateKey))
	if err != nil {
		t.Fatal(err)
	}

	b, cleanup := newBuilder(t, "")
	defer cleanup()

	b.DKIM = s

	var hdr email.Header
	hdr.Add("To", []byte("david@spilled.ink"))
	buf := b.Filer.BufferFile(0)
	defer buf.Close()
	err = b.Build(buf, &email.Msg{Headers: hdr, Parts: []email.Part{{
		Content:     strReader("Hello, World!\n"),
		ContentType: "text/plain",
		IsBody:      true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buf.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	got := string(hdr.Get("DKIM-Signature"))
	if got == "" {
		t.Fatal("missing DKIM-Signature")
	}
	if !strings.Contains(got, "h=content-type:mime-version:to;") {
		t.Errorf("signature has wrong h= headers: %q", got)
	}

	outBytes, err := ioutil.ReadAll(buf)
	if err != nil {
		t.Fatal(err)
	}
	out := string(outBytes)
	if !strings.Contains(out, "DKIM-Signature") {
		t.Errorf("missing DKIM-Signature in output:\n%s", out)
	}
}
