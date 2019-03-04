package db_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"

	"crawshaw.io/iox"

	"spilled.ink/spilldb/db"
)

func TestAuthenticator(t *testing.T) {
	filer := iox.NewFiler(0)
	defer filer.Shutdown(context.Background())

	dir, err := ioutil.TempDir("", "imapdb-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("data store tempdir: %s", dir)
	dbpool, err := db.Open(filepath.Join(dir, "spilld.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dbpool.Close()

	conn := dbpool.Get(nil)
	const username = "foo@spilled.ink"
	const devPassword = "aaaabbbbccccdddd"
	userID, err := db.AddUser(conn, db.UserDetails{
		EmailAddr: username,
		Password:  "agenericpassword",
	})
	pwd := strings.ToUpper(devPassword)
	if _, err := db.AddDevice(conn, userID, "testdevice", pwd); err != nil {
		t.Fatal(err)
	}
	dbpool.Put(conn)

	ctx := context.Background()
	var log string

	a := &db.Authenticator{
		Logf: func(format string, v ...interface{}) {
			log = fmt.Sprintf(format, v...)
		},
		Where: "test",
		DB:    dbpool,
	}
	if authUserID, err := a.AuthDevice(ctx, "remote1", username, []byte(pwd)); err != nil {
		t.Errorf("AuthDevice failed: %v", err)
	} else if userID != authUserID {
		t.Errorf("AuthDevice matched userID %d, want %d", authUserID, userID)
	}
	if log == "" {
		t.Error("log missing")
	} else if !strings.Contains(log, username) {
		t.Errorf("log does not mention username %q", username)
	}

	log = ""
	if _, err := a.AuthDevice(ctx, "", username, nil); err != db.ErrBadCredentials {
		t.Errorf("AuthDevice with bad password want ErrBadCredentials, got %v", err)
	} else if !strings.Contains(log, "bad password") {
		t.Errorf("AuthDevice with bad password want log to mention it, got %s", log)
	}
}
