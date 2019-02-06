// Package boxmgmt manages local user mailboxes.
//
// As a general principle, code should use either the main spilldb
// configuration database or the user's spillbox database.
// The few pieces of code that do need to touch both are isolated
// in this package, if possible.
package boxmgmt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/imap"
	"spilled.ink/spilldb/spillbox"
)

type BoxMgmt struct {
	filer      *iox.Filer
	spilldPool *sqlitex.Pool
	dbdir      string

	mu        sync.Mutex
	users     map[int64]*User // userID -> user
	notifiers []imap.Notifier
}

func New(filer *iox.Filer, spilldPool *sqlitex.Pool, dbdir string) (*BoxMgmt, error) {
	bm := &BoxMgmt{
		filer:      filer,
		spilldPool: spilldPool,
		dbdir:      dbdir,
		users:      make(map[int64]*User),
	}
	return bm, nil
}

func (bm *BoxMgmt) RegisterNotifier(n imap.Notifier) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.notifiers = append(bm.notifiers, n)
	for _, u := range bm.users {
		u.Box.RegisterNotifier(n)
	}
}

// Open returns an existing user's database connection.
// It returns a cached connection if the user db is already open.
// TODO: rename. We don't track openness as a resource so the name is confusing.
func (bm *BoxMgmt) Open(ctx context.Context, userID int64) (*User, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	u := bm.users[userID]
	if u != nil {
		return u, nil
	}
	u = &User{
		userID: userID,
	}

	dbfile := "file::memory:?mode=memory"
	if bm.dbdir != "" {
		dir := filepath.Join(bm.dbdir, "users")
		os.MkdirAll(dir, 0770)
		dbfile = filepath.Join(dir, fmt.Sprintf("spilld_user%d.db", userID))
	}
	box, err := spillbox.New(userID, bm.filer, dbfile, 4)
	if err != nil {
		return nil, err
	}
	for _, n := range bm.notifiers {
		box.RegisterNotifier(n)
	}

	u.Box = box
	bm.users[userID] = u
	return u, nil
}

func (bm *BoxMgmt) Close() error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	var err error
	for _, user := range bm.users {
		if uErr := user.Box.Close(); err == nil {
			err = uErr
		}
	}
	return err
}

// TODO: remove and use *spillbox.Box directly?
type User struct {
	userID int64
	Box    *spillbox.Box
}

func (u *User) UserName() string {
	return "crawshaw@spilled.ink" // TODO
}
