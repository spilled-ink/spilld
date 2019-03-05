package imapdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime/trace"
	"strings"
	"testing"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/email/msgcleaver"
	"spilled.ink/imap"
	"spilled.ink/imap/imapserver"
	"spilled.ink/imap/imaptest"
	"spilled.ink/spilldb/boxmgmt"
	"spilled.ink/spilldb/db"
)

const tracing = false

func Test(t *testing.T) {
	if tracing {
		f, err := os.Create("trace.out")
		if err != nil {
			t.Fatalf("failed to create trace output file: %v", err)
		}
		defer func() {
			if err := f.Close(); err != nil {
				t.Fatalf("failed to close trace file: %v", err)
			}
		}()

		if err := trace.Start(f); err != nil {
			t.Fatalf("failed to start trace: %v", err)
		}
		defer trace.Stop()
	}

	filer := iox.NewFiler(0)
	filer.DefaultBufferMemSize = 1 << 20
	filer.Logf = t.Logf
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		filer.Shutdown(ctx)
	}()

	t.Run("imapdb", func(t *testing.T) {
		for _, test := range imaptest.Tests {
			test := test
			t.Run(test.Name, func(t *testing.T) {
				t.Parallel()
				dataStore, err := newDataStore(filer, t.Logf)
				if err != nil {
					t.Fatal(err)
				}
				server, err := imaptest.InitTestServer(filer, dataStore, dataStore)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := server.Shutdown(); err != nil {
						t.Fatal(err)
					}
					dataStore.Close()
				}()

				test.Fn(t, server)
			})
		}
	})
}

type dataStore struct {
	backend       *backend
	dbpool        *sqlitex.Pool
	nextStagingID int64
}

func (ds *dataStore) Login(c *imapserver.Conn, username, password []byte) (int64, imap.Session, error) {
	return ds.backend.Login(c, username, password)
}

func (ds *dataStore) RegisterNotifier(notifier imap.Notifier) {
	ds.backend.RegisterNotifier(notifier)
}

func (ds *dataStore) Close() {
	ds.dbpool.Close()
}

func (ds *dataStore) AddUser(username, password []byte) (err error) {
	fmt.Printf("dataStore.AddUser(%s, %s)\n", username, password)
	ctx := context.Background()

	conn := ds.dbpool.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer ds.dbpool.Put(conn)

	userID, err := db.AddUser(conn, db.UserDetails{
		EmailAddr: string(username),
		Password:  "agenericpassword",
	})
	pwd := strings.ToUpper(string(password))
	if _, err := db.AddDevice(conn, userID, "testdevice", pwd); err != nil {
		return err
	}

	user, err := ds.backend.boxmgmt.Open(ctx, userID)
	if err != nil {
		return err
	}
	if err := user.Box.Init(ctx); err != nil {
		return err
	}
	return nil
}

func (ds *dataStore) SendMsg(date time.Time, data io.Reader) error {
	msg, err := msgcleaver.Cleave(ds.backend.filer, data)
	if err != nil {
		return fmt.Errorf("SendMsg: %v", err)
	}
	msg.Date = date

	addr := string(msg.Headers.Get("To"))
	userID, err := ds.getUserID(addr)
	if err != nil {
		return fmt.Errorf("SendMsg: %v", err)
	}

	ctx := context.Background()
	user, err := ds.backend.boxmgmt.Open(ctx, userID)
	if err != nil {
		return fmt.Errorf("SendMsg: %v", err)
	}
	done, err := user.Box.InsertMsg(ctx, msg, ds.nextStagingID)
	ds.nextStagingID++
	if err != nil {
		return err
	}
	if !done {
		return errors.New("SendMsg: missing message content")
	}
	return nil
}

func (ds *dataStore) getUserID(addr string) (int64, error) {
	conn := ds.dbpool.Get(nil)
	defer ds.dbpool.Put(conn)

	stmt := conn.Prep("SELECT UserID FROM UserAddresses WHERE Address = $addr;")
	stmt.SetText("$addr", addr)
	return sqlitex.ResultInt64(stmt)
}

func newDataStore(filer *iox.Filer, logf func(format string, v ...interface{})) (*dataStore, error) {
	dir, err := ioutil.TempDir("", "imapdb-test-")
	if err != nil {
		return nil, err
	}
	logf("data store tempdir: %s", dir)
	dbpool, err := db.Open(filepath.Join(dir, "spilld.db"))
	if err != nil {
		return nil, err
	}

	boxMgmt, err := boxmgmt.New(filer, dbpool, dir)
	if err != nil {
		return nil, fmt.Errorf("bomgmt: %v", err)
	}

	ds := &dataStore{
		backend:       NewBackend(dbpool, filer, boxMgmt, logf).(*backend),
		dbpool:        dbpool,
		nextStagingID: 42,
	}
	return ds, nil
}
