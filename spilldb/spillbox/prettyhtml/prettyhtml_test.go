package prettyhtml

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlainText(t *testing.T) {
	const html = "Here is some HTML to convert to plain text " +
		"version.<div>Next line.</div><div><br></div><div>Next&nbsp;" +
		"paragraph.</div><div><br></div><div>This is&nbsp;<b>bold</b>" +
		",&nbsp;<i>italic</i>, and&nbsp;<u>underlined</u>&nbsp;text." +
		"</div><div><br></div><div>Regards.</div>" +
		"<div><br></div>" +
		"<div><br></div>"

	want := strings.Replace(`Here is some HTML to convert to plain text version.
Next line.

Next paragraph.

This is bold, italic, and underlined text.

Regards.`, "\n", "\r\n", -1)

	buf := new(bytes.Buffer)
	if err := PlainText(buf, strings.NewReader(html)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if got != want {
		t.Errorf("PlainText()=\n%s\n\nwant:\n%s", got, want)
	}
}
