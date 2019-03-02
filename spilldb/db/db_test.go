package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crawshaw.io/iox"
	"spilled.ink/spilldb/db"
)

func TestLog(t *testing.T) {
	now := time.Now()
	l := db.Log{
		Where:    "here",
		What:     "it",
		When:     now,
		Duration: 57 * time.Millisecond,
	}
	data := make(map[string]interface{})
	if err := json.Unmarshal([]byte(l.String()), &data); err != nil {
		t.Fatal(err)
	}
	if got, want := data["where"], "here"; got != want {
		t.Errorf("where=%q, want %q", got, want)
	}
	if got, want := data["what"], "it"; got != want {
		t.Errorf("where=%q, want %q", got, want)
	}
	if got, want := data["when"], now.Format(time.RFC3339Nano); got != want {
		t.Errorf("when=%q, want %q", got, want)
	}
	if got, want := data["duration"], "57ms"; got != want {
		t.Errorf("duration=%q, want %q", got, want)
	}

	l.Err = errors.New("an error msg")
	data = make(map[string]interface{})
	if err := json.Unmarshal([]byte(l.String()), &data); err != nil {
		t.Fatal(err)
	}
	if got, want := data["err"], l.Err.Error(); got != want {
		t.Errorf("err=%q, want %q", got, want)
	}

	l.Data = map[string]interface{}{"data1": 42}
	data = make(map[string]interface{})
	if err := json.Unmarshal([]byte(l.String()), &data); err != nil {
		t.Fatal(err)
	}
	if got, want := data["data"].(map[string]interface{})["data1"], float64(42); got != want {
		t.Errorf("data=%f, want %f", got, want)
	}
}

func TestAddUser(t *testing.T) {
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
	defer dbpool.Put(conn)

	const username = "foo@spilled.ink"
	const devPassword = "aaaabbbbccccdddd"
	userID, err := db.AddUser(conn, db.UserDetails{
		Username: username,
		Password: "agenericpassword",
	})
	pwd := strings.ToUpper(devPassword)
	if _, err := db.AddDevice(conn, userID, "testdevice", pwd); err != nil {
		t.Fatal(err)
	}

	if err := db.AddUserAddress(conn, userID, "bar@spilled.ink", false); err != nil {
		t.Fatal(err)
	}
	if err := db.AddUserAddress(conn, userID, "baz@spilled.ink", false); err != nil {
		t.Fatal(err)
	}

	wantOtherAddrs := []string{"bar@spilled.ink", "baz@spilled.ink"}
	var gotOtherAddrs []string
	stmt := conn.Prep("SELECT Address, PrimaryAddr FROM UserAddresses WHERE UserID = $userID ORDER BY Address;")
	stmt.SetInt64("$userID", userID)
	for {
		if hasNext, err := stmt.Step(); err != nil {
			t.Fatal(err)
		} else if !hasNext {
			break
		}
		if stmt.GetInt64("PrimaryAddr") != 0 {
			if got, want := stmt.GetText("Address"), "foo@spilled.ink"; got != want {
				t.Errorf("primary addr is %q, want %q", got, want)
			}
			continue
		}
		gotOtherAddrs = append(gotOtherAddrs, stmt.GetText("Address"))
	}
	if !reflect.DeepEqual(wantOtherAddrs, gotOtherAddrs) {
		t.Errorf("other addrs: %v, want %v", gotOtherAddrs, wantOtherAddrs)
	}

	if err := db.AddUserAddress(conn, userID, "bop@spilled.ink", true); err != nil {
		t.Fatal(err)
	}

	wantOtherAddrs = []string{"bar@spilled.ink", "baz@spilled.ink", "foo@spilled.ink"}
	gotOtherAddrs = []string{}
	stmt = conn.Prep("SELECT Address, PrimaryAddr FROM UserAddresses WHERE UserID = $userID ORDER BY Address;")
	stmt.SetInt64("$userID", userID)
	for {
		if hasNext, err := stmt.Step(); err != nil {
			t.Fatal(err)
		} else if !hasNext {
			break
		}
		if stmt.GetInt64("PrimaryAddr") != 0 {
			if got, want := stmt.GetText("Address"), "bop@spilled.ink"; got != want {
				t.Errorf("primary addr is %q, want %q", got, want)
			}
			continue
		}
		gotOtherAddrs = append(gotOtherAddrs, stmt.GetText("Address"))
	}
	if !reflect.DeepEqual(wantOtherAddrs, gotOtherAddrs) {
		t.Errorf("other addrs: %v, want %v", gotOtherAddrs, wantOtherAddrs)
	}
}
