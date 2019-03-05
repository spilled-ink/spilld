// Package imapdb implements a spilldb IMAP backend.
package imapdb

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"spilled.ink/spilldb/db"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/email"
	"spilled.ink/email/msgcleaver"
	"spilled.ink/imap"
	"spilled.ink/imap/imapparser"
	"spilled.ink/imap/imapserver"
	"spilled.ink/spilldb/boxmgmt"
	"spilled.ink/spilldb/spillbox"
)

func NewBackend(dbpool *sqlitex.Pool, filer *iox.Filer, boxmgmt *boxmgmt.BoxMgmt, logf func(format string, v ...interface{})) imapserver.DataStore {
	return &backend{
		dbpool:  dbpool,
		filer:   filer,
		boxmgmt: boxmgmt,
		logf:    logf,
		auth: &db.Authenticator{
			DB:    dbpool,
			Logf:  logf,
			Where: "imap",
		},
	}
}

func New(tlsConfig *tls.Config, dbpool *sqlitex.Pool, filer *iox.Filer, boxmgmt *boxmgmt.BoxMgmt, logf func(format string, v ...interface{})) *imapserver.Server {
	debugDir := "/tmp/smsmtpd_imap_debug"
	os.MkdirAll(debugDir, 0700)
	debugFn := func(sessionID string) io.WriteCloser {
		name := filepath.Join(debugDir, "imap-"+sessionID+".txt")
		f, err := os.Create(name)
		if err != nil {
			logf("failed to create debug file: %v", err)
			return nil
		}
		return f
	}

	s := &imapserver.Server{
		DataStore: NewBackend(dbpool, filer, boxmgmt, logf),
		Filer:     filer,
		Logf:      logf,
		TLSConfig: tlsConfig,
		Debug:     debugFn,
	}

	return s
}

type backend struct {
	dbpool  *sqlitex.Pool
	filer   *iox.Filer
	boxmgmt *boxmgmt.BoxMgmt
	logf    func(format string, v ...interface{})
	auth    *db.Authenticator
}

func (b *backend) Login(c *imapserver.Conn, username, password []byte) (int64, imap.Session, error) {
	ctx := c.Context
	remoteAddr := ""
	if addr := c.RemoteAddr(); addr != nil {
		remoteAddr = addr.String()
	}
	userID, err := b.auth.AuthDevice(ctx, remoteAddr, string(username), password)
	if err == db.ErrBadCredentials {
		return 0, nil, imapserver.ErrBadCredentials
	} else if err != nil {
		return 0, nil, err
	}

	user, err := b.boxmgmt.Open(ctx, userID)
	if err != nil {
		return 0, nil, err
	}

	logUserPrefix := fmt.Sprintf("user%d: ", userID)
	s := &session{
		c:      c,
		userID: userID,
		name:   string(username),
		user:   user,
		filer:  b.filer,
		logf: func(format string, v ...interface{}) {
			b.logf(logUserPrefix+format, v...)
		},
		mailboxes: make(map[int64]*mailbox),
	}

	return userID, s, nil
}

func (b *backend) RegisterNotifier(n imap.Notifier) {
	b.boxmgmt.RegisterNotifier(n)
}

type session struct {
	c      *imapserver.Conn
	userID int64
	name   string
	user   *boxmgmt.User
	filer  *iox.Filer
	logf   func(format string, v ...interface{})

	mu        sync.Mutex
	mailboxes map[int64]*mailbox
}

func (s *session) Mailboxes() (mailboxes []imap.MailboxSummary, err error) {
	defer func() {
		s.logf("Mailboxes len(mailboxes)=%d", len(mailboxes))
	}()

	// TODO: subscribed
	ctx := s.c.Context
	conn := s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return nil, context.Canceled
	}
	defer s.user.Box.PoolRO.Put(conn)

	stmt := conn.Prep(`SELECT MailboxID, Name, Attrs, Subscribed
		FROM Mailboxes WHERE Name IS NOT NULL ORDER BY Name;`)
	for {
		if hasNext, err := stmt.Step(); err != nil {
			s.logf("Mailboxes: %v", err)
			return nil, err
		} else if !hasNext {
			break
		}
		mailboxes = append(mailboxes, imap.MailboxSummary{
			Name:  stmt.GetText("Name"),
			Attrs: imap.ListAttrFlag(stmt.GetInt64("Attrs")),
		})
	}
	sort.Slice(mailboxes, func(i, j int) bool {
		ni, nj := mailboxes[i].Name, mailboxes[j].Name
		if ni == "INBOX" {
			ni = ""
		}
		if nj == "INBOX" {
			nj = ""
		}
		return ni < nj
	})
	return mailboxes, nil
}

func (s *session) Mailbox(name []byte) (imap.Mailbox, error) {
	ctx := s.c.Context
	conn := s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return nil, context.Canceled
	}
	defer s.user.Box.PoolRO.Put(conn)

	stmt := conn.Prep("SELECT MailboxID, Name, Subscribed FROM Mailboxes WHERE Name = $name;")
	stmt.SetBytes("$name", name)
	if hasNext, err := stmt.Step(); err != nil {
		s.logf("Mailbox: %v", err)
		return nil, err
	} else if !hasNext {
		return nil, fmt.Errorf("mailbox not found: %s", name)
	}
	b := s.getMailbox(stmt)
	stmt.Reset()

	return b, nil
}

func (s *session) getMailbox(stmt *sqlite.Stmt) *mailbox {
	mailboxID := stmt.GetInt64("MailboxID")

	s.mu.Lock()
	m := s.mailboxes[mailboxID]
	if m == nil {
		m = &mailbox{
			s:          s,
			mailboxID:  stmt.GetInt64("MailboxID"),
			name:       stmt.GetText("Name"),
			subscribed: stmt.GetInt64("Subscribed") != 0,
		}
		s.mailboxes[mailboxID] = m
	}
	s.mu.Unlock()

	return m
}

func (s *session) CreateMailbox(nameb []byte, attr imap.ListAttrFlag) (err error) {
	ctx := s.c.Context
	conn := s.user.Box.PoolRW.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer s.user.Box.PoolRW.Put(conn)
	defer sqlitex.Save(conn)(&err)

	return spillbox.CreateMailbox(conn, string(nameb), attr)
}

func (s *session) DeleteMailbox(nameb []byte) error {
	ctx := s.c.Context
	conn := s.user.Box.PoolRW.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer s.user.Box.PoolRW.Put(conn)

	return spillbox.DeleteMailbox(conn, string(nameb))
}

func (s *session) RenameMailbox(old, new []byte) error {
	if string(old) == "INBOX" {
		return fmt.Errorf("TODO move all inbox messages to new mailbox")
	}
	panic("TODO")
}

func (s *session) RegisterPushDevice(mailbox string, device imapparser.ApplePushDevice) error {
	ctx := s.c.Context
	return s.user.Box.RegisterPushDevice(ctx, mailbox, device)
}

func (s *session) Close() {
	s.logf("Close")
}

type mailbox struct {
	s *session

	mailboxID  int64
	seqNum     uint32
	name       string
	subscribed bool
}

func (m *mailbox) ID() int64 { return m.mailboxID }

func (m *mailbox) Info() (info imap.MailboxInfo, err error) {
	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return imap.MailboxInfo{}, context.Canceled
	}
	defer m.s.user.Box.PoolRO.Put(conn)
	defer sqlitex.Save(conn)(&err)

	info.Summary = imap.MailboxSummary{
		Name: m.name,
		// TODO Subscribed
		// TODO: ListAttrFlag
	}

	stmt := conn.Prep(`SELECT count(*) FROM Msgs
		WHERE MailboxID = $id
		AND State = $msgReady;`)
	stmt.SetInt64("$msgReady", int64(spillbox.MsgReady))
	stmt.SetInt64("$id", m.mailboxID)
	msgCount, err := sqlitex.ResultInt(stmt)
	if err != nil {
		m.s.logf("Info: StatusMessage: %v", err)
		return imap.MailboxInfo{}, err
	}
	info.NumMessages = uint32(msgCount)

	stmt = conn.Prep(`SELECT NextUID, UIDValidity FROM Mailboxes WHERE MailboxID = $id;`)
	stmt.SetInt64("$id", m.mailboxID)
	if hasNext, err := stmt.Step(); err != nil {
		return imap.MailboxInfo{}, err
	} else if !hasNext {
		return imap.MailboxInfo{}, fmt.Errorf("missing mailbox db info")
	}
	info.UIDNext = uint32(stmt.GetInt64("NextUID"))
	info.UIDValidity = uint32(stmt.GetInt64("UIDValidity"))
	stmt.Reset()

	info.NumRecent = 0 // TODO

	const withSeqNumSQL = `WITH SeqNumMsgs AS (
			SELECT row_number() OVER win AS SeqNum, Flags
			FROM Msgs
			WHERE MailboxID = $mailboxID
			AND State = 1
			WINDOW win AS (ORDER BY UID)
			ORDER BY UID
		) `
	stmt = conn.Prep(withSeqNumSQL + `SELECT SeqNum FROM SeqNumMsgs
		WHERE json_extract(Flags, "$.\\Seen") IS NULL LIMIT 1;`)
	stmt.SetInt64("$mailboxID", m.mailboxID)
	if hasNext, err := stmt.Step(); err != nil {
		return imap.MailboxInfo{}, err
	} else if hasNext {
		info.FirstUnseenSeqNum = uint32(stmt.GetInt64("SeqNum"))
		stmt.Reset()
	}

	stmt = conn.Prep(`SELECT count(*) FROM Msgs
		WHERE MailboxID = $mailboxID
		AND State = 1
		AND json_extract(Flags, "$.\\Seen") IS NULL;`)
	stmt.SetInt64("$mailboxID", m.mailboxID)
	numUnseen, err := sqlitex.ResultInt64(stmt)
	if err != nil {
		return imap.MailboxInfo{}, fmt.Errorf("imapdb.Info: NumUnseen: %v", err)
	}
	info.NumUnseen = uint32(numUnseen)

	stmt = conn.Prep("SELECT max(ModSequence) FROM Msgs WHERE MailboxID = $mailboxID;")
	stmt.SetInt64("$mailboxID", m.mailboxID)
	info.HighestModSequence, err = sqlitex.ResultInt64(stmt)
	if err != nil {
		return imap.MailboxInfo{}, fmt.Errorf("imapdb.Info: HighestModSequence: %v", err)
	}

	return info, nil
}

func (m *mailbox) Append(flags [][]byte, date time.Time, data io.ReadSeeker) (uid uint32, err error) {
	var msg *email.Msg
	msg, err = msgcleaver.Cleave(m.s.filer, data)
	if err != nil {
		return 0, err
	}
	msg.MailboxID = m.mailboxID
	msg.Date = date
	for _, flag := range flags {
		msg.Flags = append(msg.Flags, string(flag))
	}
	sort.Strings(msg.Flags)

	ctx := m.s.c.Context
	// TODO: InsertMsg elides duplicates. That's not what we want?
	done, err := m.s.user.Box.InsertMsg(ctx, msg, 0)
	if err != nil {
		return 0, err
	}
	if !done {
		return 0, errors.New("imapdb: missing message content")
	}

	conn := m.s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return 0, context.Canceled
	}
	defer m.s.user.Box.PoolRO.Put(conn)

	stmt := conn.Prep("SELECT UID FROM Msgs WHERE MsgID = $msgID")
	stmt.SetInt64("$msgID", int64(msg.MsgID))
	uid64, err := sqlitex.ResultInt64(stmt)
	if err != nil {
		return 0, err
	}

	return uint32(uid64), nil
}

func (m *mailbox) Search(op *imapparser.SearchOp, fn func(imap.MessageSummary)) error {
	matcher, err := imapparser.NewMatcher(op)
	if err != nil {
		return err
	}

	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer m.s.user.Box.PoolRO.Put(conn)

	// allMsgs is the baseline set of messagse assuming no criteria.
	const allMsgs = `SELECT row_number() OVER win AS SeqNum, MsgID, UID,
		Date, HdrsBlobID, State, Flags, ModSequence, EncodedSize
		FROM Msgs
		WHERE MailboxID = $mailboxID
		AND State = $msgReady
		WINDOW win AS (ORDER BY UID)
		ORDER BY UID`

	// Construct broader WHERE clauses to limit the number of messages.
	// TODO: WHERE ...
	stmt := conn.Prep(`SELECT * FROM (` + allMsgs + `);`)
	stmt.SetInt64("$mailboxID", m.mailboxID)
	stmt.SetInt64("$msgReady", int64(spillbox.MsgReady))

	for {
		if hasNext, err := stmt.Step(); err != nil {
			return err
		} else if !hasNext {
			break
		}

		mMsg := &matchMessage{logf: m.s.logf, conn: conn, stmt: stmt}
		if !matcher.Match(mMsg) {
			continue
		}
		fn(imap.MessageSummary{
			SeqNum: uint32(stmt.GetInt64("SeqNum")),
			UID:    uint32(stmt.GetInt64("UID")),
			ModSeq: stmt.GetInt64("ModSequence"),
		})
	}
	return nil
}

type matchMessage struct {
	logf  func(format string, v ...interface{})
	conn  *sqlite.Conn
	stmt  *sqlite.Stmt
	flags map[string]int // decoded from JSON: {"flag": 1}
	hdrs  *email.Header
}

func (m *matchMessage) SeqNum() uint32    { return uint32(m.stmt.GetInt64("SeqNum")) }
func (m *matchMessage) UID() uint32       { return uint32(m.stmt.GetInt64("UID")) }
func (m *matchMessage) ModSeq() int64     { return m.stmt.GetInt64("ModSequence") }
func (m *matchMessage) RFC822Size() int64 { return m.stmt.GetInt64("EncodedSize") }
func (m *matchMessage) Date() time.Time   { return time.Unix(m.stmt.GetInt64("Date"), 0) }

func (m *matchMessage) Flag(name string) bool {
	if m.flags == nil {
		flags := make(map[string]int)
		flagsStr := m.stmt.GetText("Flags")
		if err := json.Unmarshal([]byte(flagsStr), &flags); err != nil {
			m.logf("search match flag decode: %v", err)
			return false
		}
		m.flags = flags
	}
	return m.flags[name] != 0
}

func (m *matchMessage) Header(name string) string {
	if m.hdrs == nil {
		msgID := email.MsgID(m.stmt.GetInt64("MsgID"))
		var err error
		m.hdrs, err = spillbox.LoadMsgHdrs(m.conn, msgID)
		if err != nil {
			m.logf("error in search match header decode: %v", err)
			return ""
		}
	}
	return string(m.hdrs.Get(email.CanonicalKey([]byte(name))))
}

func (m *mailbox) Fetch(useUID bool, seqs []imapparser.SeqRange, changedSince int64, fn func(imap.Message)) (err error) {
	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer m.s.user.Box.PoolRO.Put(conn)

	const withSeqNumSQL = `WITH SeqNumMsgs AS (
		SELECT row_number() OVER win AS SeqNum,
		MsgID, Seed, UID, ModSequence, Date, State, Flags, EncodedSize
		FROM Msgs
		WHERE MailboxID = $mailboxID
		AND State = 1    -- spillbox.MsgReady
		WINDOW win AS (ORDER BY UID)
		ORDER BY UID
	) `

	var stmt *sqlite.Stmt
	if useUID {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs
			WHERE UID >= $min AND UID <= $max AND ModSequence > $changedSince;`)
	} else {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs
			WHERE SeqNum >= $min AND SeqNum <= $max AND ModSequence > $changedSince;`)
	}
	stmt.SetInt64("$mailboxID", m.mailboxID)
	stmt.SetInt64("$changedSince", changedSince)

	for _, seq := range seqs {
		min, max := int64(seq.Min), int64(seq.Max)
		if max == 0 {
			max = math.MaxUint32
		}
		stmt.Reset()
		stmt.SetInt64("$min", min)
		stmt.SetInt64("$max", max)

		for {
			if hasNext, err := stmt.Step(); err != nil {
				return err
			} else if !hasNext {
				break
			}
			if err := m.fetchMsg(conn, stmt, fn); err != nil {
				stmt.Reset()
				return err
			}
		}
	}

	return nil
}

func (m *mailbox) fetchMsg(conn *sqlite.Conn, stmt *sqlite.Stmt, fn func(imap.Message)) (err error) {
	msgID := email.MsgID(stmt.GetInt64("MsgID"))
	hdrs, err := spillbox.LoadMsgHdrs(conn, msgID)
	if err != nil {
		stmt.Reset()
		return fmt.Errorf("%v headers: %v", msgID, err)
	}

	msg := &message{
		s:    m.s,
		conn: conn,
		msg: email.Msg{
			MsgID:       msgID,
			Seed:        stmt.GetInt64("Seed"),
			Date:        time.Unix(stmt.GetInt64("Date"), 0),
			Headers:     *hdrs,
			EncodedSize: stmt.GetInt64("EncodedSize"),
		},
		summary: imap.MessageSummary{
			SeqNum: uint32(stmt.GetInt64("SeqNum")),
			UID:    uint32(stmt.GetInt64("UID")),
			ModSeq: stmt.GetInt64("ModSequence"),
		},
	}

	flags := make(map[string]int)
	if err := json.NewDecoder(stmt.GetReader("Flags")).Decode(&flags); err != nil {
		stmt.Reset()
		return fmt.Errorf("%v flags: %v", msgID, err)
	}
	for flag := range flags {
		msg.msg.Flags = append(msg.msg.Flags, flag)
	}
	sort.Strings(msg.msg.Flags)

	msg.msg.Parts, err = spillbox.LoadPartsSummary(conn, msgID)
	if err != nil {
		stmt.Reset()
		return err
	}

	fn(msg)

	msg.conn = nil

	return nil
}

func (m *mailbox) Expunge(uidSeqs []imapparser.SeqRange, fn func(seqNum uint32)) (err error) {
	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRW.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer m.s.user.Box.PoolRW.Put(conn)
	defer sqlitex.Save(conn)(&err)

	stmt := conn.Prep(`SELECT COUNT(*) FROM Msgs WHERE
			MailboxID = $mailboxID
			AND State = 1
			AND json_extract(Flags, "$.\\Deleted") == 1;`)
	stmt.SetInt64("$mailboxID", m.mailboxID)
	if count, err := sqlitex.ResultInt(stmt); err != nil {
		return err
	} else if count == 0 {
		return nil
	}

	var expunged []uint32
	stmt = conn.Prep(`WITH SeqNumMsgs AS (
			SELECT row_number() OVER win AS SeqNum,
			MsgID, UID, Flags
			FROM Msgs
			WHERE MailboxID = $mailboxID
			AND State = 1
			WINDOW win AS (ORDER BY UID)
		)
		SELECT SeqNum, MsgID, UID FROM SeqNumMsgs
		WHERE json_extract(Flags, "$.\\Deleted") == 1
		ORDER BY SeqNum;`)
	stmt.SetInt64("$mailboxID", m.mailboxID)
	for {
		if hasNext, err := stmt.Step(); err != nil {
			return err
		} else if !hasNext {
			break
		}
		seqNum := uint32(stmt.GetInt64("SeqNum"))
		msgID := stmt.GetInt64("MsgID")
		uid := stmt.GetInt64("UID")
		if uidSeqs != nil && !imapparser.SeqContains(uidSeqs, uint32(uid)) {
			continue
		}

		upstmt := conn.Prep("UPDATE Msgs SET State = $msgExpunged, Expunged = $now WHERE MsgID = $msgID;")
		upstmt.SetInt64("$msgExpunged", int64(spillbox.MsgExpunged))
		upstmt.SetInt64("$now", time.Now().Unix())
		upstmt.SetInt64("$msgID", msgID)
		if _, err := upstmt.Step(); err != nil {
			return err
		}

		expunged = append(expunged, seqNum-uint32(len(expunged)))
	}

	for _, seqNum := range expunged {
		if fn != nil {
			fn(seqNum)
		}
	}

	return nil
}

/* TODO SetSubscribed:
stmt := conn.Prep("UPDATE Msgs SET Subscribed = $sub WHERE MailboxID = $mailboxID;")
stmt.SetBool("$sub", subscribed)
stmt.SetInt64("$mailboxID", b.mailboxID)
if _, err := stmt.Step(); err != nil {
	b.u.logf("SetSubscribed: %v", err)
	return err
}
*/

func (m *mailbox) HighestModSequence() (int64, error) {
	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRW.Get(ctx)
	if conn == nil {
		return 0, context.Canceled
	}
	defer m.s.user.Box.PoolRW.Put(conn)

	stmt := conn.Prep("SELECT max(ModSequence) FROM Msgs WHERE MailboxID = $mailboxID;")
	stmt.SetInt64("$mailboxID", m.mailboxID)
	modSeq, err := sqlitex.ResultInt64(stmt)
	if err != nil {
		return 0, fmt.Errorf("imapdb.HighestModSequence: %v", err)
	}
	return modSeq, nil
}

func (m *mailbox) Store(useUID bool, seqs []imapparser.SeqRange, store *imapparser.Store) (res imap.StoreResults, err error) {
	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRW.Get(ctx)
	if conn == nil {
		return imap.StoreResults{}, context.Canceled
	}
	defer m.s.user.Box.PoolRW.Put(conn)
	defer sqlitex.Save(conn)(&err)

	newModSeq, err := spillbox.NextMsgModSeq(conn, m.mailboxID)
	if err != nil {
		return imap.StoreResults{}, err
	}

	newFlags := make(map[string]bool)
	for _, flag := range store.Flags {
		if string(flag) == `\Recent` {
			continue // cannot be set by client
		}
		newFlags[string(flag)] = true
	}

	const withSeqNumSQL = `WITH SeqNumMsgs AS (
		SELECT row_number() OVER win AS SeqNum,
		MsgID, UID, Flags, ModSequence
		FROM Msgs
		WHERE MailboxID = $mailboxID
		AND State = 1    -- spillbox.MsgReady
		WINDOW win AS (ORDER BY UID)
	) `

	var stmt *sqlite.Stmt
	if useUID {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs WHERE UID >= $min AND UID <= $max ORDER BY UID;`)
	} else {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs WHERE SeqNum >= $min AND SeqNum <= $max ORDER BY UID;`)
	}
	stmt.SetInt64("$mailboxID", m.mailboxID)
	defer stmt.Reset()

	for _, seq := range seqs {
		min, max := int64(seq.Min), int64(seq.Max)
		if max == 0 {
			max = math.MaxUint32
		}
		stmt.Reset()
		stmt.SetInt64("$min", min)
		stmt.SetInt64("$max", max)

		for {
			if hasNext, err := stmt.Step(); err != nil {
				return imap.StoreResults{}, err
			} else if !hasNext {
				break
			}

			uid := uint32(stmt.GetInt64("UID"))
			seqNum := uint32(stmt.GetInt64("SeqNum"))
			modSeq := stmt.GetInt64("ModSequence")

			msgID := email.MsgID(stmt.GetInt64("MsgID"))
			flags, err := decodeFlags(stmt.GetReader("Flags"))
			if err != nil {
				return imap.StoreResults{}, err
			}
			changed := false
			switch store.Mode {
			case imapparser.StoreAdd:
				for flag := range newFlags {
					if !flags[flag] {
						changed = true
						flags[flag] = true
					}
				}
			case imapparser.StoreRemove:
				for flag := range newFlags {
					if flags[flag] {
						changed = true
						delete(flags, flag)
					}
				}
			case imapparser.StoreReplace:
				if store.UnchangedSince != 0 && modSeq > store.UnchangedSince {
					id := seqNum
					if useUID {
						id = uid
					}
					res.FailedModified = imapparser.AppendSeqRange(res.FailedModified, id)
					continue
				}
				changed = !setEq(flags, newFlags)
				flags = newFlags
			}

			flaglist := make([]string, 0, len(flags))
			for flag := range flags {
				flaglist = append(flaglist, flag)
			}
			sort.Strings(flaglist)

			if !changed {
				if store.UnchangedSince != 0 && modSeq > store.UnchangedSince {
					// RFC 7162 Section 3.1.12 offers some some subtle SHOULD behvaior.
					// For +FLAGS and -FLAGS an old modseq is fine, though the new flags
					// must be reported to the client as if they were changed.
					res.Stored = append(res.Stored, imap.StoreResult{
						SeqNum:      seqNum,
						UID:         uid,
						Flags:       flaglist,
						ModSequence: modSeq,
					})
				}
				continue
			}

			stmt := conn.Prep("UPDATE Msgs SET Flags = $flags, ModSequence = $modSeq WHERE MsgID = $msgID;")
			stmt.SetBytes("$flags", encodeFlagStrings(flaglist))
			stmt.SetInt64("$modSeq", newModSeq)
			stmt.SetInt64("$msgID", int64(msgID))
			if _, err := stmt.Step(); err != nil {
				return imap.StoreResults{}, err
			}

			res.Stored = append(res.Stored, imap.StoreResult{
				SeqNum:      seqNum,
				UID:         uid,
				Flags:       flaglist,
				ModSequence: newModSeq,
			})
		}
	}

	return res, nil
}

func setEq(s1, s2 map[string]bool) bool {
	if len(s1) != len(s2) {
		return false
	}
	if len(s1) == 0 {
		return true
	}
	for v := range s1 {
		if !s2[v] {
			return false
		}
	}
	for v := range s2 {
		if !s1[v] {
			return false
		}
	}
	return true
}

func decodeFlags(r io.Reader) (map[string]bool, error) {
	flagInts := make(map[string]int)
	if err := json.NewDecoder(r).Decode(&flagInts); err != nil {
		return nil, err
	}
	flags := make(map[string]bool)
	for flag := range flagInts {
		flags[flag] = true
	}
	return flags, nil
}

func encodeFlags(buf *bytes.Buffer, flags [][]byte) {
	buf.WriteByte('{')
	for i, flag := range flags {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprintf(buf, "%q: 1", flag)
	}
	buf.WriteByte('}')
}

func encodeFlagStrings(flags []string) []byte {
	buf := new(bytes.Buffer)
	buf.WriteByte('{')
	for i, flag := range flags {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprintf(buf, "%q: 1", flag)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func (m *mailbox) Copy(useUID bool, seqs []imapparser.SeqRange, dst imap.Mailbox, fn func(srcUID, dstUID uint32)) (err error) {
	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer m.s.user.Box.PoolRO.Put(conn)
	defer sqlitex.Save(conn)(&err)

	dstMailbox := dst.(*mailbox)

	newModSeq, err := spillbox.NextMsgModSeq(conn, dstMailbox.mailboxID)
	if err != nil {
		return err
	}

	const withSeqNumSQL = `WITH SeqNumMsgs AS (
		SELECT row_number() OVER win AS SeqNum,
		MsgID, Seed, RawHash, UID, Date, HdrsBlobID, State, Flags
		FROM Msgs
		WHERE MailboxID = $mailboxID
		AND State = 1    -- spillbox.MsgReady
		WINDOW win AS (ORDER BY UID)
	) `

	var stmt *sqlite.Stmt
	if useUID {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs WHERE UID >= $min AND UID <= $max ORDER BY UID;`)
	} else {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs WHERE SeqNum >= $min AND SeqNum <= $max ORDER BY UID;`)
	}
	stmt.SetInt64("$mailboxID", m.mailboxID)

	for _, seq := range seqs {
		min, max := int64(seq.Min), int64(seq.Max)
		if max == 0 {
			max = math.MaxUint32
		}
		stmt.Reset()
		stmt.SetInt64("$min", min)
		stmt.SetInt64("$max", max)

		for {
			if hasNext, err := stmt.Step(); err != nil {
				return err
			} else if !hasNext {
				break
			}
			if err := m.copyMsg(conn, stmt, newModSeq, dstMailbox, fn); err != nil {
				stmt.Reset()
				return err
			}
		}
	}

	return nil
}

func (m *mailbox) copyMsg(conn *sqlite.Conn, selStmt *sqlite.Stmt, newModSeq int64, dst *mailbox, fn func(srcUID, dstUID uint32)) (err error) {
	srcMsgID := email.MsgID(selStmt.GetInt64("MsgID"))
	srcUID := selStmt.GetInt64("UID")

	dstUID, err := spillbox.NextMsgUID(conn, dst.mailboxID)
	if err != nil {
		return err
	}

	// TODO: keeping this in sync with spillbox.InsertMsg is a little annoying.
	// Can we de-duplicate somehow without decoding and re-encoding headers+flags?
	stmt := conn.Prep(`INSERT INTO Msgs (
			MsgID, Seed, MailboxID, ModSequence, RawHash, State, HdrsBlobID, Date, Flags, UID
		) VALUES (
			$msgID, $seed, $mailboxID, $modSeq, $rawHash, $state, $hdrsBlobID, $date, $flags, $uid
		);`)
	stmt.SetText("$rawHash", selStmt.GetText("RawHash"))
	stmt.SetInt64("$seed", selStmt.GetInt64("Seed"))
	stmt.SetInt64("$state", int64(spillbox.MsgReady))
	stmt.SetInt64("$hdrsBlobID", selStmt.GetInt64("HdrsBlobID"))
	stmt.SetText("$flags", selStmt.GetText("Flags"))
	stmt.SetInt64("$date", selStmt.GetInt64("Date"))
	stmt.SetInt64("$mailboxID", dst.mailboxID)
	stmt.SetInt64("$modSeq", newModSeq)
	stmt.SetInt64("$uid", int64(dstUID))
	msgIDint64, err := spillbox.InsertRandID(stmt, "$msgID")
	if err != nil {
		return err
	}
	msgID := email.MsgID(msgIDint64)

	parts, err := spillbox.LoadPartsSummary(conn, srcMsgID)
	if err != nil {
		return err
	}
	m.s.logf("Copy(%s -> %s) len(parts)=%d", srcMsgID, msgID, len(parts))
	for i := range parts {
		if err := spillbox.InsertPartSummary(conn, msgID, &parts[i]); err != nil {
			return err
		}
	}

	fn(uint32(srcUID), dstUID)

	return nil
}

func (m *mailbox) Move(useUID bool, seqs []imapparser.SeqRange, dst imap.Mailbox, fn func(seqNum, srcUID, dstUID uint32)) (err error) {
	ctx := m.s.c.Context
	conn := m.s.user.Box.PoolRO.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer m.s.user.Box.PoolRO.Put(conn)
	defer sqlitex.Save(conn)(&err)

	dstMailbox := dst.(*mailbox)

	newModSeq, err := spillbox.NextMsgModSeq(conn, dstMailbox.mailboxID)
	if err != nil {
		return err
	}

	const withSeqNumSQL = `WITH SeqNumMsgs AS (
		SELECT row_number() OVER win AS SeqNum,
		MsgID, Date, UID
		FROM Msgs
		WHERE MailboxID = $mailboxID
		AND State = 1    -- spillbox.MsgReady
		WINDOW win AS (ORDER BY UID)
	) `

	var stmt *sqlite.Stmt
	if useUID {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs WHERE UID >= $min AND UID <= $max ORDER BY UID;`)
	} else {
		stmt = conn.Prep(withSeqNumSQL + `SELECT * FROM SeqNumMsgs WHERE SeqNum >= $min AND SeqNum <= $max ORDER BY UID;`)
	}
	stmt.SetInt64("$mailboxID", m.mailboxID)

	var expunged []uint32

	rangeSeqDelta := int64(0)
	for _, seq := range seqs {
		min, max := int64(seq.Min), int64(seq.Max)
		if max == 0 {
			max = math.MaxUint32
		}
		if !useUID {
			min = min - rangeSeqDelta
			max = max - rangeSeqDelta
		}
		stmt.Reset()
		stmt.SetInt64("$min", min)
		stmt.SetInt64("$max", max)

		seqDelta := uint32(0)
		for {
			if hasNext, err := stmt.Step(); err != nil {
				return err
			} else if !hasNext {
				break
			}

			srcSeqNum := uint32(stmt.GetInt64("SeqNum"))
			srcUID := uint32(stmt.GetInt64("UID"))
			msgID := stmt.GetInt64("MsgID")
			date := stmt.GetInt64("Date")

			dstUID, err := spillbox.NextMsgUID(conn, dstMailbox.mailboxID)
			if err != nil {
				return err
			}

			stmt := conn.Prep(`UPDATE Msgs SET
				MailboxID = $mailboxID, ModSequence = $modSeq, UID = $uid
				WHERE MsgID = $msgID;`)
			stmt.SetInt64("$msgID", msgID)
			stmt.SetInt64("$mailboxID", dstMailbox.mailboxID)
			stmt.SetInt64("$modSeq", newModSeq)
			stmt.SetInt64("$uid", int64(dstUID))
			if _, err := stmt.Step(); err != nil {
				return err
			}

			// Tombstone for old message.
			stmt = conn.Prep(`INSERT INTO Msgs (
					MsgID, MailboxID, Date,
					State,
					Expunged, ModSequence, UID
				) VALUES (
					$msgID, $mailboxID, $date,
					7, -- MsgExpunged
					$expunged, $modSeq, $uid
				);`)
			stmt.SetInt64("$mailboxID", m.mailboxID)
			stmt.SetInt64("$date", date)
			stmt.SetInt64("$expunged", time.Now().Unix())
			stmt.SetInt64("$modSeq", newModSeq)
			stmt.SetInt64("$uid", int64(srcUID))
			if _, err := spillbox.InsertRandID(stmt, "$msgID"); err != nil {
				return err
			}
			expungeSeqNum := srcSeqNum - seqDelta
			seqDelta++
			rangeSeqDelta++

			fn(expungeSeqNum, srcUID, dstUID)
			expunged = append(expunged, expungeSeqNum)
		}
	}

	return nil
}

func (m *mailbox) Close() error {
	return nil
}

type message struct {
	s       *session
	conn    *sqlite.Conn
	summary imap.MessageSummary
	msg     email.Msg
}

func (msg *message) Summary() imap.MessageSummary { return msg.summary }

func (msg *message) Msg() *email.Msg { return &msg.msg }

func (msg *message) LoadPart(partNum int) (err error) {
	part := &msg.msg.Parts[partNum]
	if part.Content != nil {
		return nil
	}
	if msg.conn == nil {
		return fmt.Errorf("imapdb: message connection invalidated")
	}
	return spillbox.LoadPartContent(msg.conn, msg.s.filer, part)
}

func (msg *message) SetSeen() error {
	msg.s.logf("SetSeet msgid=%s", msg.msg.MsgID)
	if msg.conn == nil {
		return fmt.Errorf("imapdb: message connection invalidated")
	}
	stmt := msg.conn.Prep(`UPDATE Msgs SET Flags = json_patch(Flags, json_object($flagname, 1)) WHERE MsgID = $msgID;`)
	stmt.SetText("$flagname", `\Seen`)
	stmt.SetInt64("$msgID", int64(msg.msg.MsgID))
	_, err := stmt.Step()
	return err
}
