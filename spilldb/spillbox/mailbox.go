package spillbox

import (
	"fmt"
	"path/filepath"
	"strings"

	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/imap"
)

func CreateMailbox(conn *sqlite.Conn, name string, attr imap.ListAttrFlag) (err error) {
	defer sqlitex.Save(conn)(&err)

	for _, res := range noKidsMailboxes {
		if strings.HasPrefix(name, res) && len(name) > len(res) && name[len(res)] == '/' {
			return fmt.Errorf("spillbox.CreateMailbox(%q): cannot create mailbox under %q", name, res)
		}
	}

	stmt := conn.Prep(`INSERT INTO Mailboxes (
			MailboxID, NextUID, UIDValidity, Name, Attrs
		) VALUES (
			$id, 1,
			coalesce((SELECT max(UIDValidity) FROM Mailboxes), 42) + 1,
			$name, $attrs);`)
	stmt.SetText("$name", name)
	stmt.SetInt64("$attrs", int64(attr))
	if _, err := InsertRandID(stmt, "$id"); err != nil {
		if sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_UNIQUE {
			return fmt.Errorf("spillbox.CreateMailbox(%q): exists", name)
		}
		return fmt.Errorf("spillbox.CreateMailbox(%q): %v", name, err)
	}

	stmt = conn.Prep(`INSERT OR IGNORE INTO MailboxSequencing
		(Name, NextModSequence) VALUES ($name, 1);`)
	stmt.SetText("$name", name)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	outer := name
	for {
		outer = filepath.Dir(outer)
		if outer == "." || outer == "INBOX" {
			break
		}

		stmt.Reset()
		stmt.SetText("$name", outer)
		_, err := InsertRandID(stmt, "$id")
		if sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_UNIQUE {
			break // outer dir exists
		}
		if err != nil {
			return fmt.Errorf("CreateMailbox(%q) outer name %q failed: %v", name, outer, err)
		}
	}

	return nil

}

func DeleteMailbox(conn *sqlite.Conn, name string) (err error) {
	if reservedMailboxNames[name] {
		return fmt.Errorf("spillbox.DeleteMailbox: cannot delete %q", name)
	}
	stmt := conn.Prep(`UPDATE Mailboxes SET DeletedName = Name, Name = NULL
		WHERE Name = $name;`)
	stmt.SetText("$name", name)
	if _, err := stmt.Step(); err != nil {
		return fmt.Errorf("spillbox.DeleteMailbox(%q): %v", name, err)
	}
	if conn.Changes() == 0 {
		return fmt.Errorf("spillbox.DeleteMailbox(%q): no such mailbox", name)
	}
	return nil
}

var noKidsMailboxes = []string{
	"INBOX",
	"Archive",
	"Sent",
	"Drafts",
	"Trash",
}

var reservedMailboxNames = map[string]bool{
	"Subscriptions": true,
}

func init() {
	for _, n := range noKidsMailboxes {
		reservedMailboxNames[n] = true
	}
}
