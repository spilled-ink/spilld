// Package htmlsafe strips an HTML document down to a small subset
// the HTML specification that is considered safe for email clients.
//
// The rules have been derived by experimentation with various email
// clients.
package htmlsafe

// TODO: allow a top-level <style> tag with careful CSS filtering.

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"spilled.ink/html/css"
	"golang.org/x/net/html"
	a "golang.org/x/net/html/atom"
)

type Tag struct {
	Attrs []a.Atom
}

type Options struct {
	AllowedTags   map[a.Atom]Tag
	AllowedStyles map[string]bool
}

var StrictEmail = &Options{
	AllowedTags: map[a.Atom]Tag{
		a.A:      Tag{Attrs: []a.Atom{a.Class, a.Href, a.Id, a.Style, a.Target}},
		a.B:      Tag{Attrs: []a.Atom{a.Class, a.Id, a.Style}},
		a.Br:     Tag{Attrs: []a.Atom{a.Class, a.Id, a.Style}},
		a.Div:    Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.Font:   Tag{Attrs: []a.Atom{a.Class, a.Color, a.Face, a.Id, a.Size, a.Style}},
		a.H1:     Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.H2:     Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.H3:     Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.H4:     Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.H5:     Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.H6:     Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.Head:   Tag{Attrs: []a.Atom{a.Dir, a.Lang}},
		a.Hr:     Tag{Attrs: []a.Atom{a.Align, a.Size, a.Width}},
		a.Img:    Tag{Attrs: []a.Atom{a.Align, a.Class, a.Height, a.Id, a.Src, a.Style, a.Usemap, a.Width}},
		a.Label:  Tag{Attrs: []a.Atom{a.Class, a.Id, a.Style}},
		a.Li:     Tag{Attrs: []a.Atom{a.Class, a.Dir, a.Id, a.Style, a.Type}},
		a.Ol:     Tag{Attrs: []a.Atom{a.Class, a.Dir, a.Id, a.Style, a.Type}},
		a.P:      Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},
		a.Span:   Tag{Attrs: []a.Atom{a.Class, a.Id, a.Style}},
		a.Strong: Tag{Attrs: []a.Atom{a.Class, a.Id, a.Style}},
		a.Table:  Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Frame, a.Id, a.Style, a.Width}},                                                 // TODO: cellpadding, cellspacing, bgcolor, border
		a.Td:     Tag{Attrs: []a.Atom{a.Abbr, a.Align, a.Class, a.Colspan, a.Dir, a.Height, a.Id, a.Lang, a.Rowspan, a.Scope, a.Style, a.Width}}, // TODO: bgcolor
		a.Th:     Tag{Attrs: []a.Atom{a.Abbr, a.Align, a.Class, a.Colspan, a.Dir, a.Height, a.Id, a.Lang, a.Scope, a.Style, a.Width}},            // TODO: bgcolor
		a.Tr:     Tag{Attrs: []a.Atom{a.Align, a.Class, a.Dir, a.Id, a.Style}},                                                                   // TODO: bgcolor
		a.U:      Tag{Attrs: []a.Atom{a.Class, a.Id, a.Style}},
		a.Ul:     Tag{Attrs: []a.Atom{a.Class, a.Dir, a.Id, a.Style}},
	},

	AllowedStyles: map[string]bool{
		"background":          true,
		"background-color":    true,
		"border":              true,
		"border-bottom":       true,
		"border-bottom-color": true,
		"border-bottom-style": true,
		"border-bottom-width": true,
		"border-color":        true,
		"border-left":         true,
		"border-left-color":   true,
		"border-left-style":   true,
		"border-left-width":   true,
		"border-right":        true,
		"border-right-color":  true,
		"border-right-style":  true,
		"border-right-width":  true,
		"border-style":        true,
		"border-top":          true,
		"border-top-color":    true,
		"border-width":        true,
		"color":               true,
		"display":             true,
		"font":                true,
		"font-family":         true,
		"font-size":           true,
		"font-style":          true,
		"font-variant":        true,
		"font-weight":         true,
		"height":              true,
		"letter-spacing":      true,
		"line-height":         true,
		"list-style-type":     true,
		"padding":             true,
		"padding-bottom":      true,
		"padding-left":        true,
		"padding-right":       true,
		"padding-top":         true,
		"table-layout":        true,
		"text-align":          true,
		"text-decoration":     true,
		"text-indent":         true,
		"text-transform":      true,
		"vertical-align":      true,
	},
}

var Safe = unionOptions(*StrictEmail, Options{
	AllowedTags: map[a.Atom]Tag{
		a.Html:  Tag{},
		a.Body:  Tag{Attrs: []a.Atom{a.Dir, a.Style}},
		a.Title: Tag{Attrs: []a.Atom{a.Dir}},
	},
})

func unionOptions(optsList ...Options) *Options {
	res := &Options{
		AllowedTags:   make(map[a.Atom]Tag),
		AllowedStyles: make(map[string]bool),
	}
	for _, opts := range optsList {
		for atom, t := range opts.AllowedTags {
			res.AllowedTags[atom] = Tag{
				Attrs: unionAttrs(res.AllowedTags[atom].Attrs, t.Attrs),
			}
		}
		for k, v := range opts.AllowedStyles {
			res.AllowedStyles[k] = v
		}
	}
	return res
}

func unionAttrs(x, y []a.Atom) (res []a.Atom) {
	m := make(map[a.Atom]struct{}, len(x)+len(y))
	for _, atom := range x {
		m[atom] = struct{}{}
	}
	for _, atom := range y {
		m[atom] = struct{}{}
	}
	for atom := range m {
		res = append(res, atom)
	}
	sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
	return res
}

type Sanitizer struct {
	RewriteURL func(attr string, url *url.URL) string
	RemovedTag func(data []byte) // TODO: more data about what was removed
	Options    *Options
	MaxBuf     int // maximum input bytes buffered, 0 means unlimited
}

// Sanitize builds a sanitized version of the HTML input.
// TODO: report if anything malicious was sanatized, like <script>
func (s *Sanitizer) Sanitize(dst io.Writer, src io.Reader) (n int, err error) {
	opts := s.Options
	if opts == nil {
		opts = Safe
	}

	discarding := false

	z := html.NewTokenizer(src)
	z.SetMaxBuf(s.MaxBuf)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}

		selfClosing := true
		switch tt {
		case html.StartTagToken:
			selfClosing = false
			fallthrough
		case html.SelfClosingTagToken:
			t := z.Token()
			allowTag, found := opts.AllowedTags[t.DataAtom]
			if found {
				discarding = false
			} else {
				discarding = true
				break
			}
			n2, err := fmt.Fprintf(dst, "<%s", t.DataAtom.String())
			n += n2
			if err != nil {
				return n, err
			}
			for _, attr := range t.Attr {
				if attr.Namespace != "" {
					continue
				}
				key := a.Lookup([]byte(attr.Key))
				if !allowTag.hasAttr(key) {
					continue
				}
				switch key {
				case a.Style:
					n2, err = s.styleAttr(dst, attr.Val, opts)
				case a.Href, a.Src:
					n2, err = s.urlAttr(dst, key, attr.Val)
				default:
					n2, err = fmt.Fprintf(dst, " %s=%q", attr.Key, attr.Val)
				}
				n += n2
				if err != nil {
					return n, err
				}
			}
			if selfClosing {
				n2, err = io.WriteString(dst, "/>")
			} else {
				n2, err = io.WriteString(dst, ">")
			}
			n += n2
			if err != nil {
				return n, err
			}
			continue
		case html.EndTagToken:
			discarding = false
			t := z.Token()
			if _, found := opts.AllowedTags[t.DataAtom]; !found {
				continue
			}
			//case html.TextToken:
		}

		if !discarding {
			n2, err := dst.Write(z.Raw())
			n += n2
			if err != nil {
				return n, err
			}
		}
	}

	if err := z.Err(); err != io.EOF {
		return n, err
	}
	return n, nil
}

func (s *Sanitizer) urlAttr(dst io.Writer, attr a.Atom, val string) (n int, err error) {
	encURL := s.rewriteURL(attr, val)
	if encURL != "" {
		return fmt.Fprintf(dst, ` %s="%s"`, attr.String(), encURL)
	}
	return 0, nil
}

func (s *Sanitizer) rewriteURL(attr a.Atom, val string) string {
	u, err := url.Parse(strings.TrimSpace(val))
	if err != nil {
		return "" // bad URL is not an I/O error
	}
	switch u.Scheme {
	case "javascript":
		return ""
	case "cid", "http", "https":
		if s.RewriteURL != nil {
			return s.RewriteURL(attr.String(), u)
		}
		return u.String()
	case "":
		return ""
	}
	return ""
}

func (s *Sanitizer) styleAttr(dst io.Writer, val string, opts *Options) (n int, err error) {
	var buf []byte

	i := 0
	errh := func(line, col, n int, msg string) {}
	p := css.NewParser(css.NewScanner(strings.NewReader(val), errh))
	var decl css.Decl
	for p.ParseDecl(&decl) {
		key := decl.Property
		if !opts.AllowedStyles[string(key)] {
			continue
		}
		if i > 0 {
			buf = append(buf, ' ')
		}
		i++

		for i := range decl.Values {
			v := &decl.Values[i]
			if v.Type == css.ValueURL {
				u := s.rewriteURL(a.Style, string(v.Value))
				v.Raw = v.Raw[:0]
				v.Value = append(v.Value[:0], u...)
			}
		}
		buf = css.AppendDecl(buf, &decl)
	}

	out := bytes.NewBuffer(make([]byte, 0, len(buf)+32))
	out.WriteString(" style=\"")
	escapeAttr(out, buf)
	out.WriteByte('"')

	return dst.Write(out.Bytes())
}

func escapeAttr(dst *bytes.Buffer, src []byte) {
	for _, c := range src {
		switch c {
		case '&':
			dst.WriteString("&amp;")
		case '\'':
			dst.WriteString("&#39;")
		case '<':
			dst.WriteString("&#lt;")
		case '>':
			dst.WriteString("&#gt;")
		case '"':
			dst.WriteString("&#34;")
		case '\r':
			dst.WriteString("&#13;")
		default:
			dst.WriteByte(c)
		}
	}
}

func (t Tag) hasAttr(attr a.Atom) bool {
	for _, tattr := range t.Attrs {
		if tattr == attr {
			return true
		}
	}
	return false
}
