package email

import (
	"bytes"
	"testing"
)

var headers = []HeaderEntry{
	{Key: "MIME-Version", Value: []byte("1.0")},
	{Key: "References", Value: []byte("<CAF34+hvV0UKJ01Pqa-E9Xi9QSLjjzV+hfZnFRE44JczwUeE2pA@mail.gmail.com> <CANb5z2JQiLm=N-Y3d05kx+ZxtmdyWsWcsqqBN3FPwrtTGev5+A@mail.gmail.com> <CAF34+htQozr5DyvKLVKUYoW=KbBG4wOLid5qbrNJeU+WdBugyQ@mail.gmail.com>")},
	{Key: "DKIM-Signature1", Value: []byte("v=1; a=rsa-sha256; c=relaxed/relaxed; d=gmail.com; s=20161025; h=mime-version:references:in-reply-to:from:date:message-id:subject:to; bh=nDDO84Aa5bgvuPxp8sn3FPt18lMSIU2wcNJn3y4hNcQ=; b=EXgFu9cpBiKsRPo/Yv/mxtQ4nr+X7t1fv1nAzrcyUyd88wOQYz9mq84XCQxHtFmqMm0YI/icb+RVMGeFfiCUR/E2aDRLCypx5O0iHqvZFDqBwBo/fGZL+H2IRgAJ3ZMtD4TjddFtnnQCOwuSlWPNHLJS6t7GmPb08yR1ayVxABvyJpwlCQ3fDN/J0rvOCMZcJzKyxs5R/ZLYG+Z6LZjSoI9hO9Ap6jD9BMimICmg1VegU97xGGWCHOX5Ev5OXmG1J5y2rdzhCGJmCAhtNObwgtfY+7J/51KbUqqFlZp+bnDKCPwAQNFrmpVyPc+Ea5Nj25l458Ib6aiGyNSohTmrWg==")},
	{Key: "DKIM-Signature2", Value: []byte("v=1; a=rsa-sha256; c=relaxed/relaxed; d=gmail.com; s=20161025; h=mime-version:references:in-reply-to:from:date:message-id:subject:to; bh=nDDO84Aa5bgvuPxp8sn3FPt18lMSIU2wcNJn3y4hNcQ=; b=EXgFu9cpBiKsRPo/Yv/mxtQ4nr+X7t1fv1nAzrcyUyd88wOQYz9mq84XCQxHtFmqMm 0YI/icb+RVMGeFfiCUR/E2aDRLCypx5O0iHqvZFDqBwBo/fGZL+H2IRgAJ3ZMtD4Tjdd FtnnQCOwuSlWPNHLJS6t7GmPb08yR1ayVxABvyJpwlCQ3fDN/J0rvOCMZcJzKyxs5R/Z LYG+Z6LZjSoI9hO9Ap6jD9BMimICmg1VegU97xGGWCHOX5Ev5OXmG1J5y2rdzhCGJmCA htNObwgtfY+7J/51KbUqqFlZp+bnDKCPwAQNFrmpVyPc+Ea5Nj25l458Ib6aiGyNSohT mrWg==")},
	{Key: "x-forefront-antispam-report", Value: []byte(antispamReportValue)},
	{Key: "From", Value: []byte(fromValue)},
	{Key: "Too-Long", Value: []byte(tooLongValue)},
	// TODO: fold this? {Key: "List-Unsubscribe", Value: []byte("<https://nyc.us10.list-manage.com/unsubscribe?u=324299544aaf40307b73ead99&id=2398r48da8&e=498459203e&c=29384292ab>, <mailto:unsubscribe-mc.us10_15749383498940307b73ead99.35298392ab-2349872398@mailin.mcsv.net?subject=unsubscribe>")},
}

const antispamReportValue = `SFV:NSPM;SFS:(10009020)(39380400002)(376002)(346002)(366004)(39840400004)(396003)(199004)(189003)(111735001)(8936002)(57306001)(81156014)(8676002)(5660300001)(81166006)(50226002)(566174002)(25786009)(6916009)(68736007)(316002)(551984002)(2906002)(97736004)(66066001)(53376002)(99936001)(606006)(99286004)(6436002)(53936002)(106356001)(54896002)(33656002)(6486002)(733005)(7736002)(54556002)(966005)(3280700002)(82746002)(6506007)(102836004)(36756003)(2900100001)(77096006)(6512007)(3660700001)(83716003)(14454004)(478600001)(86362001)(6306002)(59450400001)(5890100001)(3846002)(105586002)(236005)(53946003)(861006)(6116002)(404934003)(15398625002)(559001)(579004);DIR:OUT;SFP:1101;SCL:1;SRVR:CY1PR07MB2394;H:CY1PR07MB2393.namprd07.prod.outlook.com;FPR:;SPF:None;PTR:InfoNoRecords;A:1;MX:1;LANG:en;`

const fromValue = `reply+ZXlKMGVYQWlPaUpLVjFRaUxDSmhiR2NpT2lKU1V6VXhNaUo5LmV5SmtZWFJoSWpwN0ltbGtJam8xTmpjeU15d2lkSGx3WlNJNkltWmxaV1JpWVdOcklpd2lkWE5sY2w5cFpDSTZPREkwTjMwc0ltVjRjQ0k2TVRnMk16VTNORFUxT1gwLmFfYVN0aC1aQW9Ud0x0M0w3OXphN3JQeXQ1M05wSXhwUnJCMWRWV1VCS0gzSGNMVkFtQXJsbUVUbjBSOGp3UGN4clF6UmNXbGFTWkxOaHdvRXpSbTZ1dWhUZW9XX0xPR3hjSGl0Xzc1NDQ3WWZFamt5c25FM3NBalBSMEVWbG9qNWFxQTJSR1BmbVFlY1EyRFBPUktncEFtYU13TjFsczRZOWpNekZKTWllSmVxVW5lbGE1d1FERnhVLVh4NG5aanJxSWZwM1VsUUJHWkFFcDY3bHJnRUtvTlM4ZmRmVk1yanlURFp0UHlXS1gwOHZIemV4NDFPaWZTbUZ1d3Q4Ukhsd016ZWpxOXJaRG5FSmtaSU1Cdi1KVFlYRnZsRVlGQVRIdldOU1Fqbk1aUW1MZVk2VVM2Mm1ySmlXWHhDeGJGU1dXVFZuMHNOYnRpa0xpT1QtLWdnQQ==@automatedsystem.com`

const tooLongValue = `this_is_far_too_long_a_header_value_it_has_to_overflow_there_is_no_other_way+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+0123456789+reply+ZXlKMGVYQWlPaUpLVjFRaUxDSmhiR2NpT2lKU1V6VXhNaUo5LmV5SmtZWFJoSWpwN0ltbGtJam8xTmpjeU15d2lkSGx3WlNJNkltWmxaV1JpWVdOcklpd2lkWE5sY2w5cFpDSTZPREkwTjMwc0ltVjRjQ0k2TVRnMk16VTNORFUxT1gwLmFfYVN0aC1aQW9Ud0x0M0w3OXphN3JQeXQ1M05wSXhwUnJCMWRWV1VCS0gzSGNMVkFtQXJsbUVUbjBSOGp3UGN4clF6UmNXbGFTWkxOaHdvRXpSbTZ1dWhUZW9XX0xPR3hjSGl0Xzc1NDQ3WWZFamt5c25FM3NBalBSMEVWbG9qNWFxQTJSR1BmbVFlY1EyRFBPUktncEFtYU13TjFsczRZOWpNekZKTWllSmVxVW5lbGE1d1FERnhVLVh4NG5aanJxSWZwM1VsUUJHWkFFcDY3bHJnRUtvTlM4ZmRmVk1yanlURFp0UHlXS1gwOHZIemV4NDFPaWZTbUZ1d3Q4Ukhsd016ZWpxOXJaRG5FSmtaSU1Cdi1KVFlYRnZsRVlGQVRIdldOU1Fqbk1aUW1MZVk2VVM2Mm1ySmlXWHhDeGJGU1dXVFZuMHNOYnRpa0xpT1QtLWdnQQ==@automatedsystem.com`

var encHeadersWant = "MIME-Version: 1.0\r\n" +
	"References: <CAF34+hvV0UKJ01Pqa-E9Xi9QSLjjzV+hfZnFRE44JczwUeE2pA@mail.gmail.com>\r\n" +
	"     <CANb5z2JQiLm=N-Y3d05kx+ZxtmdyWsWcsqqBN3FPwrtTGev5+A@mail.gmail.com>\r\n" +
	"     <CAF34+htQozr5DyvKLVKUYoW=KbBG4wOLid5qbrNJeU+WdBugyQ@mail.gmail.com>\r\n" +
	"DKIM-Signature1: v=1; a=rsa-sha256; c=relaxed/relaxed; d=gmail.com; s=20161025;\r\n" +
	"     h=mime-version:references:in-reply-to:from:date:message-id:subject:to;\r\n" +
	"     bh=nDDO84Aa5bgvuPxp8sn3FPt18lMSIU2wcNJn3y4hNcQ=;\r\n" +
	"     b=EXgFu9cpBiKsRPo/Yv/mxtQ4nr+X7t1fv1nAzrcyUyd88wOQYz9mq84XCQxHtFmqMm0YI/icb+RVMGeFfiCUR/E2aDRLCypx5O0iHqvZFDqBwBo/fGZL+H2IRgAJ3ZMtD4TjddFtnnQCOwuSlWPNHLJS6t7GmPb08yR1ayVxABvyJpwlCQ3fDN/J0rvOCMZcJzKyxs5R/ZLYG+Z6LZjSoI9hO9Ap6jD9BMimICmg1VegU97xGGWCHOX5Ev5OXmG1J5y2rdzhCGJmCAhtNObwgtfY+7J/51KbUqqFlZp+bnDKCPwAQNFrmpVyPc+Ea5Nj25l458Ib6aiGyNSohTmrWg==\r\n" +
	"DKIM-Signature2: v=1; a=rsa-sha256; c=relaxed/relaxed; d=gmail.com; s=20161025;\r\n" +
	"     h=mime-version:references:in-reply-to:from:date:message-id:subject:to;\r\n" +
	"     bh=nDDO84Aa5bgvuPxp8sn3FPt18lMSIU2wcNJn3y4hNcQ=;\r\n" +
	"     b=EXgFu9cpBiKsRPo/Yv/mxtQ4nr+X7t1fv1nAzrcyUyd88wOQYz9mq84XCQxHtFmqMm\r\n" +
	"     0YI/icb+RVMGeFfiCUR/E2aDRLCypx5O0iHqvZFDqBwBo/fGZL+H2IRgAJ3ZMtD4Tjdd\r\n" +
	"     FtnnQCOwuSlWPNHLJS6t7GmPb08yR1ayVxABvyJpwlCQ3fDN/J0rvOCMZcJzKyxs5R/Z\r\n" +
	"     LYG+Z6LZjSoI9hO9Ap6jD9BMimICmg1VegU97xGGWCHOX5Ev5OXmG1J5y2rdzhCGJmCA\r\n" +
	"     htNObwgtfY+7J/51KbUqqFlZp+bnDKCPwAQNFrmpVyPc+Ea5Nj25l458Ib6aiGyNSohT\r\n" +
	"     mrWg==\r\n" +
	"x-forefront-antispam-report: " + antispamReportValue + "\r\n" +
	"From: " + fromValue + "\r\n" +
	"Too-Long: " + tooLongValue[:len(tooLongValue)-11] + "\r\n" +
	"    " + tooLongValue[len(tooLongValue)-11:] + "\r\n" +
	"\r\n"

func TestEncode(t *testing.T) {
	h := new(Header)
	for _, header := range headers {
		h.Add(header.Key, header.Value)
	}
	buf := new(bytes.Buffer)
	if _, err := h.Encode(buf); err != nil {
		t.Errorf("encode failed: %v", err)
	}

	got := buf.String()
	if got != encHeadersWant {
		t.Errorf("Encode: got:\n%q\nwant:\n%q", got, encHeadersWant)
	}
}

var keyTests = []struct {
	in, out string
}{
	{"content-id", "Content-ID"},
	{"Content-Id", "Content-ID"},
	{"never-heard-of-it", "Never-Heard-Of-It"},
	{"busted--key", "Busted--Key"},
	{"odd-_key_", "Odd-_key_"},
}

func TestCanonicalKey(t *testing.T) {
	for _, test := range keyTests {
		t.Run(test.in, func(t *testing.T) {
			if got := CanonicalKey([]byte(test.in)); got != Key(test.out) {
				t.Errorf("CanonicalKey(%q)=%q, want %q", test.in, got, test.out)
			}
		})
	}
}

func BenchmarkCanonicalKey(b *testing.B) {
	hdr := []byte("Content-Id")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		CanonicalKey(hdr)
	}
}
