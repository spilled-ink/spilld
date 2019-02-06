// TODO: this is a stub package for readability-style transformations of HTML.
// TODO: be aggresive towards  unsubscribe-based mail, e.g. remove animated GIFS
package prettyhtml

import (
	"bytes"
	"io"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"spilled.ink/html/htmlsafe"
)

type Prettifier struct {
}

func New() (*Prettifier, error) {
	p := &Prettifier{}
	return p, nil
}

type Result struct {
	HTML           string
	HasUnsubscribe bool
}

var unsubRE = regexp.MustCompile(`(?i)unsubscribe`)

// Pretty cleans up the given HTML and makes it readable.
// On error, some reasonable HTML value is always returned
func (p *Prettifier) Pretty(r io.Reader, contentLinks map[string]string) (Result, error) {
	rewrite := func(attr string, url *url.URL) string {
		if url.Scheme == "cid" && contentLinks != nil {
			return contentLinks[url.Opaque]
		}
		return url.String()
	}
	s := htmlsafe.Sanitizer{RewriteURL: rewrite}
	buf := new(bytes.Buffer)
	if _, err := s.Sanitize(buf, r); err != nil {
		return Result{HTML: "failed to sanitize"}, err
	}
	orderly := buf.String()

	res := Result{
		HTML: orderly,
	}

	node, err := html.Parse(strings.NewReader(orderly))
	if err != nil {
		return Result{HTML: orderly}, err
	}

	// Analyse body for <a href=".*">.*unsubscribe.*</a>
	inLink := false
	var findUnsub func(*html.Node)
	findUnsub = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			inLink = true // <a>
		}
		if inLink && n.Type == html.TextNode {
			if unsubRE.MatchString(n.Data) {
				res.HasUnsubscribe = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findUnsub(c)
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			inLink = false
		}
	}
	findUnsub(node)

	return res, nil
}

// PlainText processes HTML into plain text.
// ALl newlines are CRLFs, just the way email likes it.
func PlainText(dst io.Writer, src io.Reader) error {
	z := html.NewTokenizer(src)
	pendingNewlines := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); err == io.EOF {
				return nil
			} else {
				return err
			}
		case html.TextToken:
			for pendingNewlines > 0 {
				dst.Write(newline)
				pendingNewlines--
			}
			if _, err := dst.Write(z.Text()); err != nil {
				return err
			}
		case html.StartTagToken:
			tn, _ := z.TagName()
			switch {
			case len(tn) == 3 && tn[0] == 'd' && tn[1] == 'i' && tn[2] == 'v':
				fallthrough
			case len(tn) == 1 && tn[0] == 'p':
				pendingNewlines++
			}
		}
	}
}

var newline = []byte{'\r', '\n'}
