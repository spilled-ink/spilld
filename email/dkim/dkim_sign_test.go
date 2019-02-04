package dkim

import (
	"bytes"
	"fmt"
	"net/mail"
	"strings"
	"testing"
)

func TestRelaxedHeaders(t *testing.T) {
	potentialHeaders := []string{"a", "b", "c"}

	// From RFC 6376, 3.4.5.
	const msg = "A:  X \r\n" +
		"B : Y \t\r\n" +
		"\tZ  \r\n" +
		"\r\n"

	mmsg, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}

	headerKeysBuf, out := new(bytes.Buffer), new(bytes.Buffer)
	if err := collectRelaxedHeaders(headerKeysBuf, out, potentialHeaders, mmsg.Header); err != nil {
		t.Fatal(err)
	}
	headerKeys := headerKeysBuf.String()

	if want := "a:b"; headerKeys != want {
		t.Errorf("headerKeys=%q, want %q", headerKeys, want)
	}

	want := "a:X\r\n" +
		"b:Y Z\r\n"
	if got := out.String(); got != want {
		t.Errorf("out=%q, want %q", got, want)
	}
}

var bodyTests = []struct {
	body string
	hash string
}{
	{
		body: strings.Replace(`--ff7c7911124c59ff202320f18a3b36be2517cf6b041f6691a6204a69d056
Content-Type: text/html

Here is some HTML to convert to plain text version.<div>Next line.</div><div><br></div><div>Next&nbsp;paragraph.</div><div><br></div><div>This is&nbsp;<b>bold</b>,&nbsp;<i>italic</i>, and&nbsp;<u>underlined</u>&nbsp;text.</div><div><br></div><div>Regards.</div>
--ff7c7911124c59ff202320f18a3b36be2517cf6b041f6691a6204a69d056
Content-Type: text/plain

Here is some HTML to convert to plain text version.
Next line.

Next paragraph.

This is bold, italic, and underlined text.

Regards.
--ff7c7911124c59ff202320f18a3b36be2517cf6b041f6691a6204a69d056--
`, "\n", "\r\n", -1),
		hash: "oYXqSYgyGrxRT93p/bOPMxrm2ZTGd3fnMMcXhjwuPkg=", // produced by ARC-Message-Signature c=relaxed/relaxed on gmail
	},
}

func TestBodies(t *testing.T) {
	for i, bt := range bodyTests {
		bt := bt
		t.Run(fmt.Sprintf("i=%d", i), func(t *testing.T) {
			buf := new(bytes.Buffer)
			err := relaxedBodyHash(buf, strings.NewReader(bt.body))
			if err != nil {
				t.Fatal(err)
			}
			got := buf.String()
			if got != bt.hash {
				t.Errorf("hash=%s, want %s", got, bt.hash)
			}
		})
	}
}

var testPrivateKey = `-----BEGIN RSA PRIVATE KEY-----
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

func TestSigner(t *testing.T) {
	s, err := NewSigner([]byte(testPrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	s.Domain = "spilled.ink"
	s.Selector = "20180812"

	// From RFC 6376, 3.4.5.
	const msg = "From: David Crawshaw <david@spilled.ink>\r\n" +
		"To: sales@thepencilcompany.com\r\n" +
		"\r\n" +
		"Hello I would like to buy some pencils please.\r\n"
	mmsg, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := s.Sign(mmsg.Header, mmsg.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := string(sig)
	const want = "v=1; a=rsa-sha256; c=relaxed/relaxed; d=spilled.ink; " +
		"s=20180812; h=from:to; " +
		"bh=9NQdhsl2Ev6IxT84434gWZr4UlAnR+3pSUMBVeSDexo=; " +
		"b=K3Dr9z/GEQdiuNsp5/bwiq3lSoX1G/UGiiV4qpe13GYfwkPnhq5fLZGbgc+B12Y0e9 " +
		"H+5E6FlDDx1CAgT0vZovuvoyV/Cc+iiAEzoEO8JTeDBqIh5NcFVEd9z6DVYiYaZvGt " +
		"/BZD0zSVIJZtlt8XihiK6Q6o3YXOS/qx7r/GMPk="
	t.Log("len(want): ", len(want))

	if got != want {
		t.Errorf("signed header:\n%s\n\nwant:\n%s", got, want)
	}
}

func BenchmarkSigner(b *testing.B) {
	b.StopTimer()
	s, err := NewSigner([]byte(testPrivateKey))
	if err != nil {
		b.Fatal(err)
	}
	s.Domain = "spilled.ink"
	s.Selector = "20180812"

	const msgHdr = "From: David Crawshaw <david@spilled.ink>\r\n" +
		"To: sales@thepencilcompany.com\r\n" +
		"\r\n"
	const msgBody = "Hello I would like to buy some pencils please.\r\n"
	mmsg, err := mail.ReadMessage(strings.NewReader(msgHdr))
	if err != nil {
		b.Fatal(err)
	}
	hdr := mmsg.Header

	b.ReportAllocs()
	b.StartTimer()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Sign(hdr, strings.NewReader(msgBody)); err != nil {
			b.Fatal(err)
		}
	}
}
