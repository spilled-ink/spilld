package webcache

import (
	"context"
	"io"
	"net/http"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/iox/webfetch"
	"crawshaw.io/sqlite/sqlitex"
)

func New(dbpool *sqlitex.Pool, filer *iox.Filer, httpClient *http.Client, logf func(format string, v ...interface{})) (*webfetch.Client, error) {
	conn := dbpool.Get(nil)
	defer dbpool.Put(conn)

	err := sqlitex.ExecTransient(conn, `CREATE TABLE IF NOT EXISTS WebCache (
		URL         TEXT PRIMARY KEY,
		FetchTime   INTEGER NOT NULL, -- seconds since epoc, time.Now().Unix()
		ContentType TEXT,
		Content     BLOB
	);`, nil)
	if err != nil {
		return nil, err
	}

	c := cache{dbpool}
	return &webfetch.Client{
		Filer:    filer,
		Client:   httpClient,
		Logf:     logf,
		CacheGet: c.get,
		CachePut: c.put,
	}, nil
}

type cache struct {
	dbpool *sqlitex.Pool
}

func (c cache) get(ctx context.Context, dst io.Writer, url string) (bool, string, error) {
	conn := c.dbpool.Get(ctx)
	if conn == nil {
		return false, "", context.Canceled
	}
	defer c.dbpool.Put(conn)

	stmt := conn.Prep("SELECT rowid, ContentType FROM WebCache WHERE URL = $url;")
	stmt.SetText("$url", url)
	if found, err := stmt.Step(); err != nil {
		return false, "", err
	} else if !found {
		return false, "", nil
	}
	rowID := stmt.GetInt64("rowid")
	contentType := stmt.GetText("ContentType")
	stmt.Reset()

	blob, err := conn.OpenBlob("", "Webcache", "Content", rowID, false)
	if err != nil {
		return false, "", err
	}
	defer blob.Close()

	if _, err := io.Copy(dst, blob); err != nil {
		return false, "", err
	}
	return true, contentType, nil
}

func (c cache) put(ctx context.Context, url, contentType string, src io.Reader, srcLen int64) (err error) {
	conn := c.dbpool.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer c.dbpool.Put(conn)
	defer sqlitex.Save(conn)(&err)

	stmt := conn.Prep(`INSERT INTO WebCache (
			URL, FetchTime, ContentType, Content
		) VALUES (
			$url, $fetchTime, $contentType, $content
		);`)
	stmt.SetText("$url", url)
	if contentType != "" {
		stmt.SetText("$contentType", contentType)
	} else {
		stmt.SetNull("$contentType")
	}
	stmt.SetInt64("$fetchTime", time.Now().Unix())
	stmt.SetZeroBlob("$content", srcLen)
	if _, err := stmt.Step(); err != nil {
		return err
	}
	rowID := conn.LastInsertRowID()

	blob, err := conn.OpenBlob("", "Webcache", "Content", rowID, true)
	if err != nil {
		return err
	}
	_, err = io.Copy(blob, src)
	if err := blob.Close(); err != nil {
		return err
	}
	return nil
}
