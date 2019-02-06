package webcache

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
)

func mkdb(t *testing.T) *sqlitex.Pool {
	t.Helper()

	flags := sqlite.SQLITE_OPEN_READWRITE | sqlite.SQLITE_OPEN_CREATE | sqlite.SQLITE_OPEN_SHAREDCACHE | sqlite.SQLITE_OPEN_URI
	dbpool, err := sqlitex.Open("file::memory:?mode=memory&cache=shared", flags, 8)
	if err != nil {
		t.Fatal(err)
	}

	return dbpool
}

func TestWebCache(t *testing.T) {
	block := make(chan struct{})
	close(block)

	saw := make(map[string]int)

	handler := func(w http.ResponseWriter, r *http.Request) {
		saw[r.URL.Path]++
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "content")
	}
	ts := httptest.NewTLSServer(http.HandlerFunc(handler))
	defer ts.Close()

	filer := iox.NewFiler(0)
	dbpool := mkdb(t)
	defer func() {
		if err := dbpool.Close(); err != nil {
			t.Error(err)
		}
	}()

	webclient, err := New(dbpool, filer, ts.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer webclient.Shutdown(context.Background())

	do := func() {
		req, err := http.NewRequest("GET", ts.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := webclient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := ioutil.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := res.Header.Get("Content-Type"), "text/plain"; got != want {
			t.Errorf("Content-Type: %q, want %q", got, want)
		}
		if got, want := string(body), "content"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}

	do() // fills cache

	const want = 1
	if got := saw["/"]; got != want {
		t.Errorf(`saw["/"]=%d, want %d`, got, want)
	}

	do() // hits cache

	if got := saw["/"]; got != want {
		t.Errorf(`saw["/"]=%d, want %d`, got, want)
	}
}

// TODO: this is a duplicate of code in iox/webfetch/webfetch_test.go.
func TestConcurrency(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "contentof:")
		io.WriteString(w, r.URL.Path)
	}
	ts := httptest.NewTLSServer(http.HandlerFunc(handler))
	defer ts.Close()

	newReq := func(path string) *http.Request {
		req, err := http.NewRequest("GET", ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}

	filer := iox.NewFiler(0)
	dbpool := mkdb(t)
	defer func() {
		if err := dbpool.Close(); err != nil {
			t.Error(err)
		}
	}()

	webclient, err := New(dbpool, filer, ts.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer webclient.Shutdown(context.Background())

	// Concurrent cache filling.
	wg := new(sync.WaitGroup)
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			path := fmt.Sprintf("/file%d", i)
			res, err := webclient.Do(newReq(path))
			if err != nil {
				t.Fatal(err)
			}
			body, err := ioutil.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(body), "contentof:"+path; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		}()
	}
	wg.Wait()

	// Concurrent cache hits.
	wg = new(sync.WaitGroup)
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			path := fmt.Sprintf("/file%d", i)
			res, err := webclient.Do(newReq(path))
			if err != nil {
				t.Fatal(err)
			}
			body, err := ioutil.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(body), "contentof:"+path; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		}()
	}
	wg.Wait()
}
