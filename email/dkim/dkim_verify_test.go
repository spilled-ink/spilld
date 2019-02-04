package dkim

import (
	"bytes"
	"context"
	"crypto/rsa"
	"fmt"
	"io/ioutil"
	"net/mail"
	"strings"
	"testing"
)

func TestRelaxedBody(t *testing.T) {
	// From RFC 6376, 3.4.5.
	const msg = " C \r\n" +
		"D  \t E\r\n"

	r := newRelaxedBody(strings.NewReader(msg))
	out, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), " C\r\nD E\r\n"; got != want {
		t.Errorf("got=%q, want %q", got, want)
	}
}

func TestRelaxedBodyTrailingCRLFs(t *testing.T) {
	const msg = " C \r\n" +
		"\r\n"

	r := newRelaxedBody(strings.NewReader(msg))
	out, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), " C\r\n"; got != want {
		t.Errorf("got=%q, want %q", got, want)
	}

	const noTrailing = "A\r\nMessage"
	r = newRelaxedBody(strings.NewReader(noTrailing))
	out, err = ioutil.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "A\r\nMessage\r\n"; got != want {
		t.Errorf("got=%q, want %q", got, want)
	}
}

func TestSignThenVerify(t *testing.T) {
	s, err := NewSigner([]byte(testPrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	s.Domain = "spilled.ink"
	s.Selector = "20180812"

	msg := strings.Replace(`From: David Crawshaw <david@spilled.ink>
To: sales@thepencilcompany.com

Hello do you sell pencils?
`, "\n", "\r\n", -1)

	mmsg, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}

	body, err := ioutil.ReadAll(mmsg.Body)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := s.Sign(mmsg.Header, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	signedMsg := "DKIM-Signature: " + string(sig) + "\r\n" + msg

	v := &Verifier{}
	testPublicKeyHook = func(domain string) *rsa.PublicKey { return &s.key.PublicKey }
	defer func() { testPublicKeyHook = nil }()

	if err := v.Verify(context.Background(), strings.NewReader(signedMsg)); err != nil {
		t.Fatal(err)
	}
}

func TestValidSig(t *testing.T) {
	for _, test := range testValidSigs {
		t.Run(test.name, func(t *testing.T) {
			if test.skipBody {
				testSkipBody = true
				defer func() { testSkipBody = false }()
			}

			lookupTXT := func(ctx context.Context, domain string) ([]string, int, error) {
				if domain == test.txtDomain {
					return test.txtRecord, 1800, nil
				}
				return nil, 0, fmt.Errorf("unknown lookuptxt test domain: %q", domain)
			}
			v := &Verifier{
				LookupTXT: lookupTXT,
			}
			r := strings.NewReader(strings.Replace(test.msg, "\n", "\r\n", -1))
			if err := v.Verify(context.Background(), r); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var testValidSigs = []struct {
	name      string
	msg       string
	txtDomain string
	txtRecord []string
	skipBody  bool
}{
	{
		name:      "relaxed/relaxed verified by dkimvalidator.com",
		txtDomain: "20180812._domainkey.spilled.ink",
		txtRecord: []string{
			"k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA7WjkMiKWkrP6d3" +
				"urX8OzrBUQMroeQbQf/hhQ69ThhmWq6NiGseVm+Fg/6rlEF89x8tel0e" +
				"HfTE5ybFRjZ76YMOukj8Q0Wxf/V",
			"EXnSy4P+l0NBeat4LI0iFp8K/lRcRiaOoTyJ+JbGqggH6fsDgHGqTCmnXiKT2wqtS5T" +
				"ZXWQE4LOGTY4khV4sMRr5Kva/KNt6yQ/TFg+Aeolt0wcNtr0DLW6rvMg" +
				"+QJSOjjUXl1P12hvRpysnm9d7FE",
			"NIoveQA6Go940Gtu/czjE41aZhxTNfY+0OG3gruvx0dG0Qjf1v8hXMihwaYM5pt/3sj" +
				"nttdWED4OuZOT3dJi7IiDFNNGJBwIDAQAB",
		},
		msg: `Received: from localhost (spool.posticulous.com [18.206.79.126])
    by relay-1.us-west-2.relay-prod (Postfix) with ESMTPS id 9DE2A26ABD
    for <NNQNdTzhmisSkM@dkimvalidator.com>; Thu, 16 Aug 2018 18:31:59 +0000 (UTC)
Date: Thu, 16 Aug 2018 18:31:58 +0000
Subject: hello
From: "David Crawshaw" <david@spilled.ink>
To: <NNQNdTzhmisSkM@dkimvalidator.com>
Message-Id: <cv+pKrYyLdb3HmGBjcFea7JE@spool.posticulous.com>
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary=.UwNQoG6E7FG6fzjR.
DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=spilled.ink;
     s=20180812; h=content-type:date:from:message-id:subject:to;
     bh=qxBsxOpzLvv/39777ZHb4eJdqHrjJrfmr3wShyQBlXM=;
     b=QFnIXL/J/Vz7kGGyME1HDjdW/aQfXSsFXWMv+vcNXIZZuKI+37UQ5xAbfb/ZXzsKAQ
     +374IeJhyEaK9aTrQlNogM0hy9oIkLJBp75iVACI9KU7iWdzjdWpyO3p/fvOdeDE+8
     XAHP/n5yjwllmHgLohoRtASQzWgTBxzFtUyWywFrJEnJykTa6vItkajGofJ1AICmqM
     Tmut58EkCplEFCEgAia3RkpZ2E4LTDUzXuEAqG/4Mcp4nm94T/a9eYb1bFcv1iu24P
     pRBrHyZ6B6WJDl5fo1pLseX6Pu8uA4pJ2JzgxohYTPBiIKfsAL9BpC4s0YrhjEBlYE
     fmcaN3vyPu4w==

--.UwNQoG6E7FG6fzjR.
Content-Type: text/plain; charset="UTF-8"

testing
--.UwNQoG6E7FG6fzjR.
Content-Type: text/html; charset="UTF-8"

<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>testing</body></html>
--.UwNQoG6E7FG6fzjR.--
`,
	},
	{
		name:      "trailing semicolon in dkim-signature",
		txtDomain: "20180812._domainkey.spilled.ink",
		txtRecord: []string{
			`k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDlPKmFqjWCqh4kZqdAoQmOWD69
5FTqiuGNEXtADNOt2PlmRjbiLOwPJWdzTAjbABPddmPHJXDPLolEDPKbeOAdsBog
vpw6ZKvGNd5ZcXYNyX7j2oyG+RO5TbBSYWLfB1QgJWXztfUrPxWkd50CD6Ht11KA
6h31coW2JYcbtRMbpwIDAQAB`,
		},
		msg: `DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=spilled.ink;
		s=20180812; h=from:to;
		bh=9NQdhsl2Ev6IxT84434gWZr4UlAnR+3pSUMBVeSDexo=;
		b=K3Dr9z/GEQdiuNsp5/bwiq3lSoX1G/UGiiV4qpe13GYfwkPnhq5fLZGbgc+B12Y0e9
		 H+5E6FlDDx1CAgT0vZovuvoyV/Cc+iiAEzoEO8JTeDBqIh5NcFVEd9z6DVYiYaZvGt
     /BZD0zSVIJZtlt8XihiK6Q6o3YXOS/qx7r/GMPk=
From: David Crawshaw <david@spilled.ink>
To: sales@thepencilcompany.com

Hello I would like to buy some pencils please.
`,
	},
	{
		name:      "trailing semicolon in dkim-signature via gmail",
		txtDomain: "wordfly02._domainkey.roh.org.uk",
		txtRecord: []string{
			`k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC92pElhk9GQwZUfPD
+a6TO/IvIWQBDVAtVU9lFEcqr3jnD9zOvKQgy+L/LaIFKl7WMFeZi/NA6ofpGNqyWZzjYC5
f5/F+BPLj2B4fRkUTcLYSkyR4S7s8iQ/F3P9fQqsE1g7uc2E/l8lZ0rAw5qqThvFAEArrzg
LIu/4+Kl8P9TwIDAQAB`,
		},
		skipBody: true,
		msg: `DKIM-Signature: v=1; a=rsa-sha1; d=roh.org.uk; s=wordfly02; c=relaxed/relaxed;
	q=dns/txt; i=@roh.org.uk; t=1515163572;
	h=From:Subject:Date:To:MIME-Version:Content-Type;
	bh=pq6UjHDAomwxDMXzBsUYJDuD1GE=;
	b=d3klTv/MSiXoyBa0ftjEk9H0pt7I5FeRIcYqxxWI/KkYfFGMZi+LvA+HaHbHSGpA
	TWj675sFGZAvMGkoD8Ig45uXmnRUDXynXO1xws7zIwOKvSJJHPku87A6AEl3ax29
  ipIBxr4oGQcBobEyqTh05P4ispRbRsI8Ik2jH+VSMuY=;
MIME-Version: 1.0
From: "Royal Opera House" <no-response@roh.org.uk>
To: david@zentus.com
Date: 5 Jan 2018 06:46:12 -0800
Subject: Otello starring Jonas Kaufmann at Symphony Space New York
Content-Type: text/html; charset=utf-8

`,
	},
	{
		name:      "spaces inside header",
		txtDomain: "20180812._domainkey.spilled.ink",
		txtRecord: []string{
			`k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDlPKmFqjWCqh4kZqdAoQmOWD69
5FTqiuGNEXtADNOt2PlmRjbiLOwPJWdzTAjbABPddmPHJXDPLolEDPKbeOAdsBog
vpw6ZKvGNd5ZcXYNyX7j2oyG+RO5TbBSYWLfB1QgJWXztfUrPxWkd50CD6Ht11KA
6h31coW2JYcbtRMbpwIDAQAB`,
		},
		msg: `DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=spilled.ink; s=20180812; h=from:to; bh=xRuk4aw8cwxkC0iS9sfjFUMhelw0aozc+9nBdFQ7I+E=; b=rdDxMT7oDNxslazlP5zgxQ5b7p2RcDLDzaV+rrof3PFfZzKshrDLzcNiZaoZDLxZb3 D3SemQ4kssc7pyjG908XiGHdrX2HL4Z/mlrtnnrROnY2v0nzkZibSQVrww2eCXXBWe nvpvJXcWeZgM6M8lqnYfs6unxwi9AJ7qiAmnQAo=
From: David Crawshaw <david@spilled.ink>
To: Many    spaces    <sales@thepencilcompany.com>

Hello do you sell pencils?
`,
	},
	{
		name:      "relaxed header keep space in bh= field",
		txtDomain: "selector_ups._domainkey.ups.com",
		txtRecord: []string{
			`v=DKIM1; k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC
xRsmyNnf4mwdNjA+O0dNL7yIIoHCLV60puou6/5PSYLQsa/U43p5HL7AXebOun5K8o
KG5uQ1vYtiHtw4gxEjyaqbEFfTTZf5B/ZCQnHhrIg31F5jSspfZtxlcf4zdQBQW5Fn
ZTpbotjzyxs34G1+8WQz+smTunzNeG/rZLczBEwIDAQAB`,
		},
		skipBody: true,
		msg: `DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed;
	d=ups.com; s=selector_ups; t=1516825031; h=From:
	 To:Subject:MIME-Version:Content-Type:Date:
	 Message-ID; bh=HV4wO6TA6sTLeBTnuyvkcs8/be
	f0SJIZhhnGsXyv+kY=; b=gV7G1Z4ijhGeN5GxSmUw+mg6eyF0
	F/W1wGVzWzWH4r3XrdCa24nQhV1/xNns7OEeJPSOxN/caF+xF0
	S3Jji6VOqK5aqnZad4MgUEEgqQc6UFejZNA3bsAHAvNQhSatdO
	+nI5LkKIv8Sash4zikVP9/NTL94hY071ZzqYoVhbkBc=
From: "UPS Quantum View" <pkginfo@ups.com>
To: DAVID@ZENTUS.COM
Subject: UPS Update: Package Scheduled for Delivery Tomorrow
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary=----NextPart_1516825031534.0000591400
Date: Wed, 24 Jan 2018 15:17:11 -0500 (EST)
Message-ID: <2014097605.1201163.1516825031538.JavaMail.beawl@drevil-vip.ups.com>

`,
	},

	{
		name:      "simple header with trailing CRLF on DKIM-Signature",
		txtDomain: "corp._domainkey.verizon.com",
		txtRecord: []string{
			"v=DKIM1; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKB" +
				"gQC8kHcekuSRCOdlYuKKHHufWYGlXAd80ivzvXDpklq8CnBxGnPMTEXPua9" +
				"pCezMMrhjtU2GcKyCMMOBtEhJIRogVHhLiE73WntesqEEQ2s2tg8DpAupUR" +
				"zFxlvzHHYsCFheYDraGhbVhYlegzUiXlliOA0t7BWXx0kZsABrwevJBQIDAQAB;",
		},
		skipBody: true,
		msg: `DKIM-Signature: v=1; a=rsa-sha256; c=simple/simple;
  d=verizon.com; i=@verizon.com; q=dns/txt; s=corp;
  t=1518659859; x=1550195859;
  h=from:to:message-id:subject:mime-version:
   content-transfer-encoding;
  bh=849VOtmXirlxpvdULCrsgXyRK5KDHZFtuTumJMT+8H8=;
  b=Q6xwLwpl73Zd7ao88r4NbvuG0ioE+HQzrUqjeDsDqONRyZo7kE1SAjLx
   oVvVEvu/srnc/aHru6xrtidDfUcwixr3ziXLcHnnqXRFPRAEGHragbE7u
   w9nh6s+zpnFTll6R5E0kSgxd3+sTPQN0pTF+vR0d7v/3i+zwcT8+0gX7+
   U=;
From: Fios <verizon-services@verizon.com>
To: david@zentus.com
Message-ID: <511470382.1336350.1518659858703.JavaMail.weblogic@dcfdpemail.verizon.com>
Subject: Important information enclosed
MIME-Version: 1.0
Content-Transfer-Encoding: quoted-printable

`,
	},
	{
		name:      "simple/simple spam verified by gmail",
		txtDomain: "bdk._domainkey.e.altonlane.com",
		txtRecord: []string{
			"v=DKIM1; k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADC" +
				"BiQKBgQC8/67gwG587+WPnexnMxk+JoMgMCynZk+hMRRxCKyO" +
				"dTJ1LMdQolwXN2iZCyyBq8jkqXev9xp012Ahpey7urYhj1Lr0q" +
				"ktoDxIJMm6mCv1rmtMtXpiLVBo6cXoDlNwLqQSARfyCAWLexm" +
				"rg1n5HUPejNucmLigfxyDo9bwOlSXDwIDAQAB",
		},
		msg: `ARC-Authentication-Results: i=1; mx.google.com;
       dkim=pass header.i=@e.altonlane.com header.s=bdk header.b=iNjTZQjl;
       spf=pass (google.com: domain of e9wymknqd4ysmyvgq0gtglthoce9qs77-b@e.altonlane.com designates 216.27.63.222 as permitted sender) smtp.mailfrom=e9wymknqd4ysmyvgq0gtglthoce9qs77-b@e.altonlane.com
Return-Path: <e9wymknqd4ysmyvgq0gtglthoce9qs77-b@e.altonlane.com>
Received: from ms222.bronto.com (ms222.bronto.com. [216.27.63.222])
        by mx.google.com with ESMTPS id m31si189127qtd.323.2018.02.02.12.02.18
        for <david@zentus.com>
        (version=TLS1_2 cipher=ECDHE-RSA-AES128-GCM-SHA256 bits=128/128);
        Fri, 02 Feb 2018 12:02:21 -0800 (PST)
Received-SPF: pass (google.com: domain of e9wymknqd4ysmyvgq0gtglthoce9qs77-b@e.altonlane.com designates 216.27.63.222 as permitted sender) client-ip=216.27.63.222;
DKIM-Signature: v=1; a=rsa-sha256; c=simple; s=bdk; d=e.altonlane.com;
 h=Content-Transfer-Encoding:Content-Type:Date:From:List-Unsubscribe:
 Message-ID:MIME-Version:Reply-To:Subject:To; i=email@e.altonlane.com;
 bh=goH5gMe0OyyyvGszj1AUpdAD9cGj9uH4w9iAlHeCzO8=;
 b=iNjTZQjluWEReSOvh9ZFiSjHlYZw8QIESIsM7hnx0c582ofoIlcEyko8ENcmoFnGbNFT+e/8Xzq6
   E0olx9pAx0QmWuq7g4i96DlT/ROODjOl8IabdMuuYilIJRcAhbrWxwE7ryKfUKREynf6Y/kFJFVg
   CHNlE31j0DIgURCJs5U=
Received: from localhost (10.0.2.125) by ms222.bronto.com id hej1so2d2f4f for <david@zentus.com>; Fri, 2 Feb 2018 15:02:13 -0500 (envelope-from <e9wymknqd4ysmyvgq0gtglthoce9qs77-b@e.altonlane.com>)
Content-Transfer-Encoding: 7BIT
Content-Type: multipart/alternative; boundary="==Multipart_Boundary_xc75j85x"
Date: Fri, 2 Feb 2018 15:02:12 -0500
From: =?UTF-8?Q?Alton=20Lane?= <email@e.altonlane.com>
List-Unsubscribe: <mailto:e9wymknqd4ysmyvgq0gtglthoce9qs77-u@e.altonlane.com>, <http://e.altonlane.com/public/webform/render_form/default/425e6c4d300471399d4be9c301cdda3a/unsub/89een3hs87s9417s4ko6e56kazlsy/bfxrvmhtvhvchbgwkiklhomvzawlbfc?td=Wn-NX8yIavtns8KfEs5QowfdJPGtnTdvK7NnapEAedczwHueaGay1rOzuJfG0JYA5_8nL4Skz91Qr934-YRTAUGQeTPG_CKG1_5KIJ908VTnKzxgByAHAKBVh1mLOvgSOSYUx5gJHyNsd2whfqJSaZ3Lq4Z9_mmrv7&tid=215031210476000509717909712096273801409550690541689328962750842044947008539536632553475>
Message-ID: <e9wymknqd4ysmyvgq0gtglthoce9qs77.s77.1517601732@e.altonlane.com>
MIME-Version: 1.0
Reply-To: =?UTF-8?Q?Alton=20Lane?= <email@e.altonlane.com>
Return-Path: e9wymknqd4ysmyvgq0gtglthoce9qs77-b@e.altonlane.com
Subject: =?utf-8?Q?SUPER=20BONUS=20CHALLENGE=20=7C=20Pick=20your=20team=2C=20win=20your=20wardrobe?=
To: <david@zentus.com>
X-campaignID: bm23_bfxrvmhtvhvchbgwkiklhomvzawlbfc
X-Mailer: BM23 Mail

--==Multipart_Boundary_xc75j85x
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: 7bit

You have received the alternative text version of an HTML message. Please click below to access the web version of the message:

View an HTML version of this message: http://e.altonlane.com/public/viewmessage/html/36547/e9wymknqd4ysmyvgq0gtglthoce9q/0be203eb00000000000000000000000633f4



Alton Lane | 11 West 25th St, 5th FL | New York, NY 10010 | United States

Visit the link below to unsubscribe from future marketing messages from Alton Lane
http://e.altonlane.com/public/webform/render_form/default/425e6c4d300471399d4be9c301cdda3a/unsub/89een3hs87s9417s4ko6e56kazlsy/bfxrvmhtvhvchbgwkiklhomvzawlbfc?td=Wn-NX8yIavtns8KfEs5QowfdJPGtnTdvK7NnapEAedczwHueaGay1rOzuJfG0JYA5_8nL4Skz91Qr934-YRTAUGQeTPG_CKG1_5KIJ908VTnKzxgByAHAKBVh1mLOvgSOSYUx5gJHyNsd2whfqJSaZ3Lq4Z9_mmrv7&tid=215031210476000509717909712096273801409550690541689328962750842044947008539536632553475

--==Multipart_Boundary_xc75j85x
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: 7bit

<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
<meta name="viewport" content="width=device-width">
<style type="text/css">

        .full-width {
            width: 640px;
        }
        table.editor-body {
  width: 100%;
}
table.row {
  padding: 0px;
  width: 100%;
  position: relative;
}
table.row td.last {
  padding-right: 0px;
}
table.columns,
table.column {
  padding-right: 0;
  margin: 0 auto;
}
.el-button table.el-wrapper {
  border-collapse: separate;
}
td.wrapper {
  padding: 0;
  position: relative;
}
.hide-for-desktop {
  display: none;
  mso-hide: all;
}
.hide-for-desktop table,
.hide-for-desktop td,
.hide-for-desktop img,
.hide-for-desktop a {
  mso-hide: all;
}
.loop-item > table {
  table-layout: auto;
}
@media (max-width: 600px) {
  .hide-for-desktop {
    mso-hide: none;
  }
  table.hide-for-desktop {
    display: table !important;
  }
  td.hide-for-desktop {
    display: table-cell !important;
  }
  img.hide-for-desktop {
    display: block !important;
  }
  a.hide-for-desktop {
    display: block !important;
  }
  table.editor-body img {
    height: auto !important;
  }
  table.editor-body center {
    min-width: 0 !important;
  }
  table.editor-body .row,
  table.editor-body .loop-item {
    width: 100% !important;
    display: block !important;
  }
  table.editor-body .wrapper {
    display: block !important;
    padding-right: 0 !important;
  }
  table.editor-body .loop-item.no-stack,
  table.editor-body .wrapper.no-stack {
    display: table-cell !important;
  }
  table.editor-body .columns,
  table.editor-body .column,
  table.editor-body .column-content {
    table-layout: fixed !important;
    float: none !important;
    width: 100% !important;
    padding-right: 0px !important;
    padding-left: 0px !important;
    display: block !important;
  }
  table.editor-body .wrapper.first .columns,
  table.editor-body .wrapper.first .column {
    display: table !important;
  }
  table.editor-body .hide-for-small,
  .hide-for-mobile,
  table.editor-body .row.hide-for-mobile {
    display: none !important;
    max-height: 0 !important;
    line-height: 0 !important;
    mso-hide: all;
  }
  td.import-element-block,
  .full-width {
    width: 100% !important;
    padding: 0;
  }
}
body {
  margin: 0;
}
.editor-body {
  font-family: "Helvetica", "Helvetica Neue", "Arial", sans-serif;
  margin: 0;
}
table {
  border-collapse: collapse;
}
ul,
ol {
  list-style-position: outside;
  margin: 0;
  padding: 0 0 0 2em;
}
.header-footer {
  margin: 0;
}
.valign {
  vertical-align: top;
}
.no-padding {
  padding-right: 0;
  padding-top: 0;
  padding-bottom: 0;
  padding-left: inherit;
  /* Padding-left:0 triggers image scale in outlook */
}
.row {
  word-break: break-word;
}
.button {
  border: 0;
  border-width: 0;
  border-color: none;
  border-style: none;
  outline: 0;
  box-sizing: border-box;
  display: block;
}
.align-left {
  text-align: left;
}
.align-right {
  text-align: right;
}
/*# sourceMappingURL=/assets/app/shared/html_editor/css/email/full.css.map */
            <style type="text/css">

		/* What it does: Remove spaces around the email design added by some email clients. */
		/* Beware: It can remove the padding / margin and add a background color to the compose a reply window. */
        html,
        body {

            background-color:#d5dade;
        }

        

    </style>
<!--[if gte mso 12]>
        <style>
           ul, ol {
               margin-left:2em !important;
            }
        </style>
    <![endif]--><!--[if gte mso 9]>
        <style>
            table.twentyPercent,
            table.two,
            table.three,
            table.four,
            table.six,
            table.nine {
                width: 100% !important;
            }
        </style>
    <![endif]-->
</head>
<body style="margin:0;background-color:#d5dade;">

<table class="editor-body" style='border-collapse:collapse;font-family:"Helvetica", "Helvetica Neue", "Arial", sans-serif;width:100%;'><tr>
<td align="center" valign="top" style="padding:0;">
            <table class="import-message" border="0" cellpadding="0" cellspacing="0" style="font-size:16px;text-align:left;align:center;border-collapse:collapse;"><tr>
<td>
                            <table cellpadding="0" cellspacing="0" class="row import-container import-container-1 import-container-3712353027" style="border-spacing:0px;border-collapse:collapse;word-break:break-word;padding:0px;width:100%;position:relative;"><tr>
<td class="wrapper  valign " style="vertical-align:top;padding:0;position:relative;">
        <table class="columns import-column import-column-1" style="width:640px;border-collapse:collapse;padding-right:0;margin:0 auto;" cellpadding="0" cellspacing="0"><tr>
<td class="column-content valign no-padding" style="width:640px;vertical-align:top;padding-right:0;padding-top:0;padding-bottom:0;padding-left:inherit;">
                    <table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-1 import-element-26015871872 el-outer el-width" style="align:left;text-align:left;font-size:0;height:30px;padding:0;line-height:normal;font-family:Helvetica,Arial,sans-serif;width:100%;" align="left">
            <div class="el-inner" style="align:left;text-align:left;font-size:0;height:30px;padding:0;line-height:normal;font-size:1px;line-height:1px;width:100%">
                 
            </div>
        </td>
    </tr></table>
<table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-2 import-element-79053449126 el-outer" style="align:center;text-align:left;font-size:16px;padding:0;line-height:normal;margin:auto;text-align:center;font-family:Helvetica,Arial,sans-serif;" align="center">
                <img src="http://hosting.fyleio.com/36547/public/Evergreen/Logonavy200.png?c=1510936710058" width="640" class="el-inner image" style="display:block;margin-right:auto;margin-left:auto;border:none;min-width:140PX;max-width:140PX;height:auto;">
</td>
    </tr></table>
<table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-3 import-element-10838709677 el-outer el-width" style="align:left;text-align:left;font-size:0;height:30px;padding:0;line-height:normal;font-family:Helvetica,Arial,sans-serif;width:100%;" align="left">
            <div class="el-inner" style="align:left;text-align:left;font-size:0;height:30px;padding:0;line-height:normal;font-size:1px;line-height:1px;width:100%">
                 
            </div>
        </td>
    </tr></table>
</td>
            </tr></table>
</td>

                                </tr></table>
<table cellpadding="0" cellspacing="0" class="row import-container import-container-2 import-container-16255541038" style="border-spacing:0px;border-collapse:collapse;word-break:break-word;padding:0px;width:100%;position:relative;"><tr>
<td class="wrapper  valign " style="vertical-align:top;padding:0;position:relative;">
        <table class="columns import-column import-column-1" style="width:640px;border-collapse:collapse;padding-right:0;margin:0 auto;" cellpadding="0" cellspacing="0"><tr>
<td class="column-content valign no-padding" style="width:640px;vertical-align:top;padding-right:0;padding-top:0;padding-bottom:0;padding-left:inherit;">
                    <table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-1 import-element-52715526764 el-outer" style="align:left;text-align:left;font-size:16px;padding:20px;background-color:#ffffff;line-height:normal;margin-left:0;font-family:Helvetica,Arial,sans-serif;" align="left">
                <a href="http://e.altonlane.com/t/l?ssid=36547&subscriber_id=avpgzitboxxqilfohrxpxztvglnbbmf&delivery_id=bfxrvmhtvhvchbgwkiklhomvzawlbfc&td=vRBiYsiUrmnFjod30tvAOQGQo1b5r-TlBG7Sm12pY5aaGmeRo7D6dq_PzJKJ9oGJAWXTndTnkyp0f3yQzaLiiOuX3U2OmYcaZhxMTWmgBzXVmjrEybbzrL-hMw6cDa4YS99BUp2Gmqv7Y-MD20u1Ysma2KbPjHvjTxBvNsPN8lT5bVzn84EI0oO9Lk-PGwJQjrVWg8PSvyu_86hJRBco7Xjycea3tmh2TQsq2zhu9YI9iD26fTeq0gFLTVydNQLS0a5jKULFDM_4Tclh8n98IJgo8PcpEfbX24" style="display:inline;" target="_blank" border="0">
                <img src="http://hosting.fyleio.com/36547/public/2018/SpecialEvent/SuperBonus2-min.jpg?c=1517598780831" width="600" class="el-inner image" style="display:block;border:none;min-width:100%;max-width:100%;height:auto;"></a>
        </td>
    </tr></table>
</td>
            </tr></table>
</td>

                                </tr></table>
<table cellpadding="0" cellspacing="0" class="row import-container import-container-3 import-container-97792215212" style="border-spacing:0px;border-collapse:collapse;word-break:break-word;padding:0px;width:100%;position:relative;"><tr>
<td class="wrapper  valign " style="vertical-align:top;padding:0;position:relative;">
        <table class="columns import-column import-column-1" style="width:640px;border-collapse:collapse;padding-right:0;margin:0 auto;" cellpadding="0" cellspacing="0"><tr>
<td class="column-content valign no-padding" style="width:640px;vertical-align:top;padding-right:0;padding-top:0;padding-bottom:0;padding-left:inherit;">
                    <table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-1 import-element-46613780572 el-outer el-width" style="align:left;text-align:left;font-size:0;height:10px;padding:0;line-height:normal;font-family:Helvetica,Arial,sans-serif;width:100%;" align="left">
            <div class="el-inner" style="align:left;text-align:left;font-size:0;height:10px;padding:0;line-height:normal;font-size:1px;line-height:1px;width:100%">
                 
            </div>
        </td>
    </tr></table>
<table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-2 import-element-56627852921 el-outer el-width" style="align:left;text-align:left;font-size:0;height:20px;padding:0;line-height:normal;font-family:Helvetica,Arial,sans-serif;width:100%;" align="left">
            <div class="el-inner" style="align:left;text-align:left;font-size:0;height:20px;padding:0;line-height:normal;font-size:1px;line-height:1px;width:100%">
                 
            </div>
        </td>
    </tr></table>
<table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-3 import-element-5584111274 el-outer" style="align:center;text-align:left;font-size:16px;padding:0;line-height:normal;margin:auto;text-align:center;font-family:Helvetica,Arial,sans-serif;" align="center">
                <img src="http://hosting.fyleio.com/36547/public/Evergreen/Evergreen_FooterBars6e818b.png?c=1510936800588" width="512" class="el-inner image" style="display:block;margin-right:auto;margin-left:auto;border:none;min-width:80%;max-width:80%;height:auto;">
</td>
    </tr></table>
<table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-4 import-element-12251074038 el-outer el-width" style="align:left;text-align:left;font-size:0;height:20px;padding:0;line-height:normal;font-family:Helvetica,Arial,sans-serif;width:100%;" align="left">
            <div class="el-inner" style="align:left;text-align:left;font-size:0;height:20px;padding:0;line-height:normal;font-size:1px;line-height:1px;width:100%">
                 
            </div>
        </td>
    </tr></table>
<table cellpadding="0" cellspacing="0" width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-5 import-element-18765773094" style="align:left;text-align:left;font-size:16px;padding:0;line-height:normal;font-family:Helvetica,Arial,sans-serif;width:640px;" align="left">
            <p width="530px" style="font-family: sans-serif;  font-weight:200; font-size:10px; line-height:12px; text-align: center;color:#6e818b;"> © 2017 Alton Lane, All rights reserved.
                                    
                                    <br><br>Book a private appointment online, or by calling <a href="tel:18888008616" style="color: #6e818b;text-decoration:underline;">888.800.8616</a><br><br> Alton Lane  |  <font style="font-family: sans-serif;  font-weight:200; font-size:10px; line-height:12px; text-align: center;color:#6e818b;">11 West 25th St, 5th FL </font>  |  <font style="font-family: sans-serif;  font-weight:200; font-size:10px; line-height:12px; text-align: center;color:#6e818b;">New York, NY 10010</font>
                                    <br><br><a target="_blank" href="http://e.altonlane.com/t/l?ssid=36547&subscriber_id=avpgzitboxxqilfohrxpxztvglnbbmf&delivery_id=bfxrvmhtvhvchbgwkiklhomvzawlbfc&td=X84F0cxKrbjIWKF4dydL7wsU1_v8iBoAt8nAT9_zHr_twpF6c2kb7HU9W7z6IGFUQf1FuBY4JxYu2fWUgAVWPMETcYxX1FY4h1dgKdzpe_7v_U9SrFlo2IGDpSAKsoUuVS8lhA6vQcLhhl1exySMSNCPxi-AKA0DlS6MGEucP6qklUb1BCXLWYIkTj7ulMKLo86hv-fCufeZmlL_-0nXjBdUW9X4wrmwaX6dWGFycPEMJxkUOJGAM41Q" style="color: #6e818b;text-decoration:underline;">facebook</a> | <a target="_blank"
href="http://e.altonlane.com/t/l?ssid=36547&subscriber_id=avpgzitboxxqilfohrxpxztvglnbbmf&delivery_id=bfxrvmhtvhvchbgwkiklhomvzawlbfc&td=l4TT5tN6F21nnK1B0rIdBwLVyfSUCTZAeTBWg1eh6b7gFIrhjd3PJ3E98_Wfj03rNZMW3jDUYYioE-zVyqM474GjDRUgIZEdRvWoCzcHyp4owP3W94p6zFbWokCp7REZk9o6BodZBlYSQdIKQo-vm--M93yOcuw8UsgHfVaV4C6ZfXjzbSJZLx_o_KmUgCwIqV0tQAjLBIZz20iM8-Y4ZC9D5qtMKWBtC8BTjXuP9JNCOmI9J7-6ke5g" style="color: #6e818b;text-decoration:nunderline;">instagram</a> | <a target="_blank"
href="http://e.altonlane.com/t/l?ssid=36547&subscriber_id=avpgzitboxxqilfohrxpxztvglnbbmf&delivery_id=bfxrvmhtvhvchbgwkiklhomvzawlbfc&td=2r4BjZNg3toocHndSstHXQu4tB1wKHfxU48cPa5raw2TkNpQzKgCbtyLJJ3_5KQSfftqTbCVSq93TATAWbRU-sprXC8SGJWSnLkgdvuaYqZfigrmIaniknM-XPq2icoL0JrIIfG3rjQtCRoajRaSiF_krPPy1WxrcOt-sjdbn3xrtlYbhg3E1NFYzXFsJE6tyQZYf-8jvkUI2Dw63aQ0Qp7KoLrCV2pzbaJlN3Js4FfR-p9fCyUMDePw" style="color: #6e818b;text-decoration:underline;">linkedin</a> | <a target="_blank"
href="http://e.altonlane.com/public/webform/render_form/default/425e6c4d300471399d4be9c301cdda3a/unsub/89een3hs87s9417s4ko6e56kazlsy/bfxrvmhtvhvchbgwkiklhomvzawlbfc?td=Wn-NX8yIavtns8KfEs5QowfdJPGtnTdvK7NnapEAedczwHueaGay1rOzuJfG0JYA5_8nL4Skz91Qr934-YRTAUGQeTPG_CKG1_5KIJ908VTnKzxgByAHAKBVh1mLOvgSOSYUx5gJHyNsd2whfqJSaZ3Lq4Z9_mmrv7&tid=215031210476000509717909712096273801409550690541689328962750842044947008539536632553475" style="color: #6e818b;text-decoration:underline;"> unsubscribe </a>
                                </p>
        </td>
    </tr></table>
<table width="100%" style="table-layout:fixed;border-collapse:collapse;"><tr>
<td class="import-element import-element-block import-element-6 import-element-82110695685 el-outer el-width" style="align:left;text-align:left;font-size:0;height:20px;padding:0;line-height:normal;font-family:Helvetica,Arial,sans-serif;width:100%;" align="left">
            <div class="el-inner" style="align:left;text-align:left;font-size:0;height:20px;padding:0;line-height:normal;font-size:1px;line-height:1px;width:100%">
                 
            </div>
        </td>
    </tr></table>
</td>
            </tr></table>
</td>

                                </tr></table>
</td>
                </tr></table>
</td>
    </tr></table>
<div style="font-family:monospace;letter-spacing:636px;line-height:0;mso-hide:all" class="hide-for-mobile"> </div>
<div style="white-space:nowrap;font:15px monospace;line-height:0;mso-hide:all" class="hide-for-mobile">                                                           </div>
<table class='editor-body'><tr><td class='no-padding' valign='top' align='center'><table class='import-message' border='0' cellpadding='0' cellspacing='0'><tr><td class='no-padding'><table class='row import-container'><tr><td class='wrapper valign'><table class='columns full-width' cellpadding='0' cellspacing='0'><tr><td class='column-content valign no-padding full-width'><table width='100%'><tr><td class='import-element import-element-block full-width no-padding'><img src="http://e.altonlane.com/t/o?ssid=36547&subscriber_id=avpgzitboxxqilfohrxpxztvglnbbmf&delivery_id=bfxrvmhtvhvchbgwkiklhomvzawlbfc&td=Wn-NX8yIavtns8KfEs5QowfdJPGtnTdvK7NnapEAedczwHueaGay1rOzuJfG0JYA5_8nL4Skz91Qr934-YRTAUGQeTPG_CKG1_5KIJ908VTnKzxgByAHAKBVh1mLOvgSOSYUx5gJHyNsd2whfqJSaZ3Lq4Z9_mmrv7" width="0" height="0"
border="0" style="visibility: hidden !important; display:none !important; max-height: 0; width: 0; line-height: 0; mso-hide: all;" alt=""/></td><td class='expander'></td></tr></table></td></tr></table></td></tr></table></td></tr></table></td></tr></table>
</body>
</html>


--==Multipart_Boundary_xc75j85x--
`,
	},
}

func BenchmarkVerify(b *testing.B) {
	b.StopTimer()

	s, err := NewSigner([]byte(testPrivateKey))
	if err != nil {
		b.Fatal(err)
	}
	s.Domain = "spilled.ink"
	s.Selector = "20180812"

	msg := strings.Replace(`From: David Crawshaw <david@spilled.ink>
To: sales@thepencilcompany.com

Hello do you sell pencils?
`, "\n", "\r\n", -1)

	mmsg, err := mail.ReadMessage(strings.NewReader(msg))
	if err != nil {
		b.Fatal(err)
	}

	body, err := ioutil.ReadAll(mmsg.Body)
	if err != nil {
		b.Fatal(err)
	}
	sig, err := s.Sign(mmsg.Header, bytes.NewReader(body))
	if err != nil {
		b.Fatal(err)
	}

	signedMsg := "DKIM-Signature: " + string(sig) + "\r\n" + msg

	testPublicKeyHook = func(domain string) *rsa.PublicKey { return &s.key.PublicKey }
	defer func() { testPublicKeyHook = nil }()

	b.ReportAllocs()
	b.StartTimer()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v := &Verifier{}
		if err := v.Verify(context.Background(), strings.NewReader(signedMsg)); err != nil {
			b.Fatal(err)
		}
	}
}
