package htmlembed

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crawshaw.io/iox"
)

// TODO: include a repeated URL
// TODO: check a ref="nofollow"
// TODO: we can do better for the cid of missing content

const msgText = `<b>Rich</b> text. Have some images:
<img src="https://example.com/foo.gif" />
<img src="https://example.com/bar.gif" />
<img src="https://example.com/doesnotexist.gif" />
`

var wantText = `<b>Rich</b> text. Have some images:
<img src="cid:` + testAssets[0].cid + `"/>
<img src="cid:` + testAssets[1].cid + `"/>
<img src="cid:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=@spilled.ink"/>
`

var testAssets = []struct {
	name      string
	contents  string
	loaderror string
	cid       string
}{
	{
		name:     "foo.gif",
		contents: `R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAEALAAAAAABAAEAAAgEAAMEBAA7`,
		cid:      "Ys-wVAiOKaDldrQ0AwwjbGEBrwWZ5vVc_omzWmGG-6Q=@spilled.ink",
	},
	{
		name:     "bar.gif",
		contents: `R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7`,
		cid:      "7xlVrnV8i5ZsgySDUDMb06MPZYztEfOH-OvwWrM2hik=@spilled.ink",
	},
	{
		name:      "doesnotexist.gif",
		loaderror: "404",
	},
}

func TestEmbedder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	filer := iox.NewFiler(0)
	defer filer.Shutdown(ctx)
	defer cancel()

	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var errstr string
		out := make([]byte, 0, 128)
		for _, a := range testAssets {
			if a.name == r.URL.Path[1:] {
				n, err := base64.StdEncoding.Decode(out[:cap(out)], []byte(a.contents))
				if err != nil {
					t.Fatalf("%s decode: %v", a.name, err)
				}
				out = out[:n]
				errstr = a.loaderror
				break
			}
		}
		if errstr != "" {
			http.Error(w, "not found", 404)
			return
		}
		if len(out) == 0 {
			t.Errorf("unknown http server path: %s", r.URL.String())
			http.Error(w, "missing", 500)
			return
		}
		w.Write(out)
	}))

	msgText := strings.Replace(msgText, "https://example.com", s.URL, -1)
	e := NewEmbedder(filer, s.Client())
	res, err := e.Embed(ctx, strings.NewReader(msgText))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assets) != len(testAssets) {
		t.Fatalf("len(res.Assets)=%d, want 2", len(res.Assets))
	}

	gotHTML, err := ioutil.ReadAll(res.HTML)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(gotHTML), wantText; got != want {
		t.Errorf("HTML=%q\nwant=%q", got, want)
	}

	for i, asset := range res.Assets {
		a := testAssets[i]
		if asset.LoadError != nil {
			if a.loaderror == "" {
				t.Errorf("%s: unexpected load error: %v", a.name, asset.LoadError)
				continue
			}
			if !strings.Contains(asset.LoadError.Error(), a.loaderror) {
				t.Errorf("%s load error %v does not mention %q", a.name, asset.LoadError, a.loaderror)
			}
			continue
		}
		if a.loaderror != "" {
			t.Errorf("%s: missing load error: %q", a.name, a.loaderror)
			continue
		}
		if got, want := mustBase64(t, a.name, asset.Bytes), a.contents; got != want {
			t.Errorf("%s=%q, want %q", a.name, got, want)
		}
		if a.cid != asset.CID {
			t.Errorf("%s CID=%s, want %s", a.name, asset.CID, a.cid)
		}
	}
}

func mustBase64(t *testing.T, name string, r io.Reader) string {
	buf := new(bytes.Buffer)
	wc := base64.NewEncoder(base64.StdEncoding, buf)
	if _, err := io.Copy(wc, r); err != nil {
		t.Fatalf("copying %s: %v", name, err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("closing copy of %s: %v", name, err)
	}
	return buf.String()
}
