// Package htmlembed fetches all the assets referenced in an HTML document.
package htmlembed

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"crawshaw.io/iox"
	"golang.org/x/net/html"
	"spilled.ink/html/htmlsafe"
)

type Embedder struct {
	userAgent string
	filer     *iox.Filer
	httpc     Doer
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

func NewEmbedder(filer *iox.Filer, httpc Doer) *Embedder {
	return &Embedder{
		userAgent: "Spilled_Ink_FetchBot/1.0",
		filer:     filer,
		httpc:     httpc,
	}
}

type Asset struct {
	CID         string
	URL         string
	Name        string
	ContentType string
	Hash        [sha256.Size]byte
	Bytes       *iox.BufferFile
	LoadError   error
}

type HTML struct {
	HTML   *iox.BufferFile
	Assets []Asset
	Links  []string
}

type work struct {
	n   *html.Node
	url string
}

// Embed returns a sanitized version of a block of HTML with any
// external resources collected in the style of multipart/mixed.
func (p *Embedder) Embed(ctx context.Context, r io.Reader) (res *HTML, err error) {
	const maxRead = 1 << 19
	r = io.LimitReader(r, maxRead)

	res = &HTML{}

	buf := p.filer.BufferFile(0)
	defer buf.Close()

	// First pass: collect assets to fetch.
	rewriteFn := func(attr string, url *url.URL) string {
		if attr == "href" {
			return url.String()
		}
		if url.Scheme == "cid" {
			return url.String()
		}
		cid := fmt.Sprintf("fetchasset%d", len(res.Assets))
		res.Assets = append(res.Assets, Asset{
			CID:   cid,
			URL:   url.String(),
			Name:  path.Base(url.Path),
			Bytes: p.filer.BufferFile(0),
		})
		return "cid:" + cid
	}
	s := &htmlsafe.Sanitizer{
		RewriteURL: rewriteFn,
		Options:    htmlsafe.Safe,
		MaxBuf:     maxRead,
	}
	if _, err := s.Sanitize(buf, r); err != nil {
		return nil, err // I/O error
	}
	if _, err := buf.Seek(0, 0); err != nil {
		return nil, err
	}

	done := make(chan struct{}, len(res.Assets))
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for i := range res.Assets {
		a := &res.Assets[i]
		go func() {
			defer func() {
				done <- struct{}{}
			}()
			p.fetch(ctx, a)
		}()
	}
	for range res.Assets {
		<-done
	}

	// Second pass. Rename assets with unique hash ID.
	rewriteFn = func(attr string, url *url.URL) string {
		if url.Scheme != "cid" {
			return url.String()
		}
		var assetNum int
		if _, err := fmt.Sscanf(url.String(), "cid:fetchasset%d", &assetNum); err != nil {
			return url.String()
		}
		if assetNum < 0 || assetNum > len(res.Assets) {
			return url.String() // uh oh
		}

		hash := res.Assets[assetNum].Hash[:]
		cid := fmt.Sprintf("%s@spilled.ink", base64.URLEncoding.EncodeToString(hash))
		res.Assets[assetNum].CID = cid

		return "cid:" + cid
	}
	res.HTML = p.filer.BufferFile(0)
	s = &htmlsafe.Sanitizer{
		RewriteURL: rewriteFn,
		Options:    htmlsafe.Safe,
		MaxBuf:     maxRead,
	}
	if _, err := s.Sanitize(res.HTML, buf); err != nil {
		return nil, err // I/O error
	}
	if _, err := res.HTML.Seek(0, 0); err != nil {
		return nil, err
	}

	return res, nil
}

func (p *Embedder) fetch(ctx context.Context, a *Asset) {
	defer func() {
		if a.LoadError != nil && a.Bytes != nil {
			a.Bytes.Close()
			a.Bytes = nil
		}
	}()

	req, err := http.NewRequest("GET", a.URL, nil)
	if err != nil {
		a.LoadError = err
		return
	}
	req.Header.Set("User-Agent", p.userAgent)
	res, err := p.httpc.Do(req.WithContext(ctx))
	if err != nil {
		a.LoadError = err
		return
	}
	defer func() {
		if err := res.Body.Close(); a.LoadError == nil {
			a.LoadError = err
		}
	}()
	if res.StatusCode != 200 {
		a.LoadError = fmt.Errorf("%d: %s", res.StatusCode, res.Status)
		return
	}
	h := sha256.New()
	if _, err := io.Copy(a.Bytes, io.TeeReader(res.Body, h)); err != nil {
		a.LoadError = fmt.Errorf("body copy failed %v", err)
		return
	}
	h.Sum(a.Hash[:0])
	a.Bytes.Seek(0, 0)

	a.ContentType = res.Header.Get("Content-Type")
	if a.ContentType == "" {
		bufPrefix := make([]byte, 512)
		n, err := io.ReadAtLeast(a.Bytes, bufPrefix, len(bufPrefix))
		if err != nil && err != io.ErrUnexpectedEOF {
			a.Bytes.Close()
			a.Bytes = nil
			a.LoadError = fmt.Errorf("content-type detection failed: %v", err)
			return
		}
		a.Bytes.Seek(0, 0)
		bufPrefix = bufPrefix[:n]
		a.ContentType = http.DetectContentType(bufPrefix)
	}

	if a.ContentType == "image/jpg" { // sigh
		a.ContentType = "image/jpeg"
	}

	switch a.ContentType {
	case "image/gif",
		"image/jpeg",
		"image/png",
		"image/svg+xml":
	default:
		a.LoadError = fmt.Errorf("unexpected content-type: %v", a.ContentType)
	}
}
