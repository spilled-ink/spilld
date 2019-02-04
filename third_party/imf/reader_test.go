// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package imf

// Originally from go/src/net/textproto/reader_test.go.

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"spilled.ink/email"
)

func reader(s string) *Reader {
	return NewReader(bufio.NewReader(strings.NewReader(s)))
}

func TestReadLineSlice(t *testing.T) {
	r := reader("line1\nline2\n")
	s, err := r.readLineSlice()
	if string(s) != "line1" || err != nil {
		t.Fatalf("Line 1: %s, %v", s, err)
	}
	s, err = r.readLineSlice()
	if string(s) != "line2" || err != nil {
		t.Fatalf("Line 2: %s, %v", s, err)
	}
	s, err = r.readLineSlice()
	if string(s) != "" || err != io.EOF {
		t.Fatalf("EOF: %s, %v", s, err)
	}
}

func TestReadContinuedLineSlice(t *testing.T) {
	const contents = "line1\nline\n 2\nline3\n"
	r := reader(contents)
	s, err := r.readContinuedLineSlice()
	if string(s) != "line1" || err != nil {
		t.Fatalf("Line 1: %s, %v", s, err)
	}
	if got, want := r.NumRead(), 6; got != want {
		t.Errorf("Line 1: read %d bytes, want %d", got, want)
	}
	s, err = r.readContinuedLineSlice()
	if string(s) != "line 2" || err != nil {
		t.Fatalf("Line 2: %s, %v", s, err)
	}
	if got, want := r.NumRead(), 6+8; got != want {
		t.Errorf("Line 2: read %d bytes, want %d", got, want)
	}
	s, err = r.readContinuedLineSlice()
	if string(s) != "line3" || err != nil {
		t.Fatalf("Line 3: %s, %v", s, err)
	}
	if got, want := r.NumRead(), len(contents); got != want {
		t.Errorf("Line 3: read %d bytes, want %d", got, want)
	}
	s, err = r.readContinuedLineSlice()
	if string(s) != "" || err != io.EOF {
		t.Fatalf("EOF: %s, %v", s, err)
	}
}

func TestReadMIMEHeader(t *testing.T) {
	const contents = "my-key: Value 1  \r\nLong-key: Even \n Longer Value\r\nmy-Key: Value 2\r\n\n"
	r := reader(contents)
	m, err := r.ReadMIMEHeader()
	want := mkHeader(
		"My-Key", "Value 1",
		"Long-Key", "Even Longer Value",
		"My-Key", "Value 2",
	)
	if got, want := r.NumRead(), len(contents)-strings.Count(contents, "\r"); got != want {
		t.Errorf("NumRead()=%d, want %d", got, want)
	}
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadMIMEHeader: %v, %v; want %v", m, err, want)
	}
}

func TestReadMIMEHeaderSingle(t *testing.T) {
	r := reader("Foo: bar\n\n")
	m, err := r.ReadMIMEHeader()
	want := mkHeader("Foo", "bar")
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadMIMEHeader: %v, %v; want %v", m, err, want)
	}
}

func TestReadMIMEHeaderNoKey(t *testing.T) {
	r := reader(": bar\ntest-1: 1\n\n")
	m, err := r.ReadMIMEHeader()
	want := mkHeader("Test-1", "1")
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadMIMEHeader: %v, %v; want %v", m, err, want)
	}
}

func TestLargeReadMIMEHeader(t *testing.T) {
	data := make([]byte, 16*1024)
	for i := 0; i < len(data); i++ {
		data[i] = 'x'
	}
	sdata := string(data)
	r := reader("Cookie: " + sdata + "\r\n\n")
	m, err := r.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("ReadMIMEHeader: %v", err)
	}
	cookie := string(m.Get("Cookie"))
	if cookie != sdata {
		t.Fatalf("ReadMIMEHeader: %v bytes, want %v bytes", len(cookie), len(sdata))
	}
}

// Test that we read slightly-bogus MIME headers seen in the wild,
// with spaces before colons, and spaces in keys.
func TestReadMIMEHeaderNonCompliant(t *testing.T) {
	// Invalid HTTP response header as sent by an Axis security
	// camera: (this is handled by IE, Firefox, Chrome, curl, etc.)
	r := reader("Foo: bar\r\n" +
		"Content-Language: en\r\n" +
		"SID : 0\r\n" +
		"Audio Mode : None\r\n" +
		"Privilege : 127\r\n\r\n")
	m, err := r.ReadMIMEHeader()
	want := mkHeader(
		"Foo", "bar",
		"Content-Language", "en",
		"Sid", "0",
		"Audio mode", "None",
		"Privilege", "127",
	)
	if !reflect.DeepEqual(m, want) || err != nil {
		t.Fatalf("ReadMIMEHeader =\n%v, %v; want:\n%v", m, err, want)
	}
}

func TestReadMIMEHeaderMalformed(t *testing.T) {
	inputs := []string{
		"No colon first line\r\nFoo: foo\r\n\r\n",
		" No colon first line with leading space\r\nFoo: foo\r\n\r\n",
		"\tNo colon first line with leading tab\r\nFoo: foo\r\n\r\n",
		" First: line with leading space\r\nFoo: foo\r\n\r\n",
		"\tFirst: line with leading tab\r\nFoo: foo\r\n\r\n",
		"Foo: foo\r\nNo colon second line\r\n\r\n",
	}

	for _, input := range inputs {
		r := reader(input)
		if m, err := r.ReadMIMEHeader(); err == nil {
			t.Errorf("ReadMIMEHeader(%q) = %v, %v; want nil, err", input, m, err)
		}
	}
}

// Test that continued lines are properly trimmed. Issue 11204.
func TestReadMIMEHeaderTrimContinued(t *testing.T) {
	// In this header, \n and \r\n terminated lines are mixed on purpose.
	// We expect each line to be trimmed (prefix and suffix) before being concatenated.
	// Keep the spaces as they are.
	r := reader("" + // for code formatting purpose.
		"a:\n" +
		" 0 \r\n" +
		"b:1 \t\r\n" +
		"c: 2\r\n" +
		" 3\t\n" +
		"  \t 4  \r\n\n")
	m, err := r.ReadMIMEHeader()
	if err != nil {
		t.Fatal(err)
	}
	want := mkHeader(
		"A", "0",
		"B", "1",
		"C", "2 3 4",
	)
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("ReadMIMEHeader mismatch.\n got: %q\nwant: %q", m, want)
	}
}

type readResponseTest struct {
	in       string
	inCode   int
	wantCode int
	wantMsg  string
}

var clientHeaders = strings.Replace(`Host: golang.org
Connection: keep-alive
Cache-Control: max-age=0
Accept: application/xml,application/xhtml+xml,text/html;q=0.9,text/plain;q=0.8,image/png,*/*;q=0.5
User-Agent: Mozilla/5.0 (X11; U; Linux x86_64; en-US) AppleWebKit/534.3 (KHTML, like Gecko) Chrome/6.0.472.63 Safari/534.3
Accept-Encoding: gzip,deflate,sdch
Accept-Language: en-US,en;q=0.8,fr-CH;q=0.6
Accept-Charset: ISO-8859-1,utf-8;q=0.7,*;q=0.3
COOKIE: __utma=000000000.0000000000.0000000000.0000000000.0000000000.00; __utmb=000000000.0.00.0000000000; __utmc=000000000; __utmz=000000000.0000000000.00.0.utmcsr=code.google.com|utmccn=(referral)|utmcmd=referral|utmcct=/p/go/issues/detail
Non-Interned: test

`, "\n", "\r\n", -1)

var serverHeaders = strings.Replace(`Content-Type: text/html; charset=utf-8
Content-Encoding: gzip
Date: Thu, 27 Sep 2012 09:03:33 GMT
Server: Google Frontend
Cache-Control: private
Content-Length: 2298
VIA: 1.1 proxy.example.com:80 (XXX/n.n.n-nnn)
Connection: Close
Non-Interned: test

`, "\n", "\r\n", -1)

func BenchmarkReadMIMEHeader(b *testing.B) {
	b.ReportAllocs()
	var buf bytes.Buffer
	br := bufio.NewReader(&buf)
	r := NewReader(br)
	for i := 0; i < b.N; i++ {
		var want int
		var find email.Key
		if (i & 1) == 1 {
			buf.WriteString(clientHeaders)
			want = 10
			find = "Cookie"
		} else {
			buf.WriteString(serverHeaders)
			want = 9
			find = "Via"
		}
		h, err := r.ReadMIMEHeader()
		if err != nil {
			b.Fatal(err)
		}
		if len(h.Index) != want {
			b.Fatalf("wrong number of headers: got %d, want %d", len(h.Index), want)
		}
		if _, ok := h.Index[find]; !ok {
			b.Fatalf("did not find key %s", find)
		}
	}
}
