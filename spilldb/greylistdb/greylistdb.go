package greylistdb

import (
	"context"
	"fmt"
	"net"
	"time"

	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/smtp/smtpserver/greylist"
)

const dbSQL = `
CREATE TABLE IF NOT EXISTS Greylist (
	RemoteAddr TEXT NOT NULL,    -- text form of IP address
	FromAddr   TEXT NOT NULL,    -- user@domain
	ToAddr     TEXT NOT NULL,    -- user@domain
	LastSeen   INTEGER NOT NULL, -- seconds since unix epoch

	PRIMARY KEY (RemoteAddr, FromAddr, ToAddr)
);
`

// New creates a new Greylist.
//
// After calling New, the caller needs to set the remaining
// exported fields of Greylist before using the NewMessage method.
func New(dbpool *sqlitex.Pool) (*greylist.Greylist, error) {
	conn := dbpool.Get(nil)
	defer dbpool.Put(conn)
	if err := sqlitex.ExecScript(conn, dbSQL); err != nil {
		return nil, fmt.Errorf("greylistdb.New: %v", err)
	}

	db := &greyDB{
		dbpool: dbpool,
	}

	gl := &greylist.Greylist{
		Whitelist: db.whitelist,
		Blacklist: db.blacklist,
		GreyDB:    db,
	}
	return gl, nil
}

type greyDB struct {
	dbpool *sqlitex.Pool
}

func (db *greyDB) Get(ctx context.Context, remoteAddr, from, to string) (time.Time, error) {
	conn := db.dbpool.Get(ctx)
	if conn == nil {
		return time.Time{}, context.Canceled
	}
	defer db.dbpool.Put(conn)

	stmt := conn.Prep(`SELECT LastSeen FROM Greylist WHERE RemoteAddr = $remoteAddr AND FromAddr = $from AND ToAddr = $to;`)
	stmt.SetText("$remoteAddr", remoteAddr)
	stmt.SetText("$from", from)
	stmt.SetText("$from", to)
	if has, err := stmt.Step(); err != nil {
		return time.Time{}, err
	} else if !has {
		return time.Time{}, greylist.ErrNotFound
	}
	t := time.Unix(stmt.GetInt64("LastSeen"), 0)
	stmt.Reset()

	return t, nil
}

func (db *greyDB) Put(ctx context.Context, remoteAddr, from, to string) error {
	conn := db.dbpool.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer db.dbpool.Put(conn)

	t := time.Now().Unix()

	stmt := conn.Prep(`INSERT INTO Greylist (
			LastSeen, RemoteAddr, FromAddr, ToAddr
		) VALUES (
			$lastSeen, $remoteAddr, $fromAddr, $toAddr
		) ON CONFLICT (RemoteAddr, FromAddr, ToAddr)
		DO UPDATE Set LastSeen=$lastSeen;`)
	stmt.SetInt64("$lastSeen", t)
	stmt.SetText("$remoteAddr", remoteAddr)
	stmt.SetText("$from", from)
	stmt.SetText("$from", to)
	_, err := stmt.Step()
	return err
}

func (db *greyDB) whitelist(ctx context.Context, remoteAddr net.Addr, from []byte) (bool, error) {
	return false, nil
}

func (db *greyDB) blacklist(ctx context.Context, remoteAddr net.Addr, from []byte) (bool, error) {
	return false, nil
}
