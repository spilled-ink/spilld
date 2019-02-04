package imaptest

import (
	"errors"
	"fmt"
	"io"
	"net/mail"
	"reflect"
	"sort"
	"sync"
	"time"

	"crawshaw.io/iox"
	"spilled.ink/email"
	"spilled.ink/email/msgcleaver"
	"spilled.ink/imap"
	"spilled.ink/imap/imapparser"
	"spilled.ink/imap/imapserver"
)

type MemoryStore struct {
	Filer *iox.Filer

	mu            sync.Mutex // guards users map, not the contents of *memoryUser
	users         map[string]*memoryUser
	nextSessionID int64
	notifiers     []imap.Notifier
}

func (s *MemoryStore) RegisterNotifier(n imap.Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.notifiers = append(s.notifiers, n)
}

func (s *MemoryStore) AddUser(uname, pass []byte) error {
	s.mu.Lock()
	username, password := string(uname), string(pass)
	if s.users == nil {
		s.users = make(map[string]*memoryUser)
		s.nextSessionID = 1
	}
	if s.users[username] != nil {
		s.mu.Unlock()
		return fmt.Errorf("MemoryStore: user %q already exists", username)
	}
	user := &memoryUser{
		id:              int64(len(s.users) + 1),
		name:            username,
		password:        password,
		mailboxes:       make(map[string]*memoryMailbox),
		uidValidityNext: 500000 + uint32(1000*len(s.users)),
		modSequenceNext: 900000 + int64(1000*len(s.users)),
	}
	s.users[username] = user
	s.mu.Unlock()

	_, session, err := s.Login(nil, uname, pass)
	if err != nil {
		return fmt.Errorf("MemoryStore: user %q initial session failed: %v", username, err)
	}
	defer session.Close()

	mboxes := []struct {
		name string
		attr imap.ListAttrFlag
	}{
		{"INBOX", 0},
		{"Archive", imap.AttrArchive},
		{"Drafts", imap.AttrDrafts},
		{"Subscriptions", 0},
		{"Sent", imap.AttrSent},
		{"Spam", imap.AttrJunk},
		{"Trash", imap.AttrTrash},
	}
	for _, mbox := range mboxes {
		if err := session.CreateMailbox([]byte(mbox.name), mbox.attr); err != nil {
			return err
		}
	}

	return nil
}

func (s *MemoryStore) SendMsg(date time.Time, data io.Reader) error {
	f := s.Filer.BufferFile(0)
	defer f.Close()
	if _, err := io.Copy(f, data); err != nil {
		return err
	}
	f.Seek(0, 0)
	msg, err := msgcleaver.Cleave(s.Filer, f)
	if err != nil {
		return fmt.Errorf("MemoryStore.SendMsg: %v", err)
	}
	to, err := mail.ParseAddress(string(msg.Headers.Get("To")))
	if err != nil {
		return fmt.Errorf("MemoryStore.SendMsg: %v", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[to.Address]
	if user == nil {
		return fmt.Errorf("MemoryStore.SendMsg: no such user %q", to.Address)
	}
	inbox := user.mailboxes["INBOX"]
	f.Seek(0, 0)
	if _, err = inbox.Append(nil, date, f); err != nil {
		return err
	}
	for _, n := range s.notifiers {
		go n.Notify(user.id, inbox.ID(), "INBOX", nil)
	}
	return err
}

func (s *MemoryStore) Login(c *imapserver.Conn, username, password []byte) (int64, imap.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.users[string(username)]
	if user == nil {
		return 0, nil, fmt.Errorf("MemoryStore: no such user %q", string(username))
	}
	if user.password != string(password) {
		return 0, nil, fmt.Errorf("MemoryStore: bad password for user %q", string(username))
	}

	session := &memorySession{
		id:     s.nextSessionID,
		server: s,
		user:   user,
	}
	s.nextSessionID++
	return user.id, session, nil
}

func (s *MemoryStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, user := range s.users {
		for _, m := range user.mailboxes {
			for i := range m.msgs {
				m.msgs[i].emailMsg.Close()
			}
		}
	}
}

type memoryUser struct {
	id       int64
	name     string
	password string

	mu              sync.Mutex
	mailboxes       map[string]*memoryMailbox
	nextMailboxID   int64
	uidValidityNext uint32
	modSequenceNext int64
}

type memorySession struct {
	id     int64
	server *MemoryStore
	user   *memoryUser
}

func (s *memorySession) Mailboxes() (summaries []imap.MailboxSummary, err error) {
	s.user.mu.Lock()
	defer s.user.mu.Unlock()
	for _, m := range s.user.mailboxes {
		summaries = append(summaries, imap.MailboxSummary{
			Name:  m.name,
			Attrs: m.attrs,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		n1, n2 := summaries[i].Name, summaries[j].Name
		if n1 == "INBOX" {
			n1 = ""
		}
		if n2 == "INBOX" {
			n2 = ""
		}
		return n1 < n2
	})
	return summaries, nil
}

func (s *memorySession) Mailbox(name []byte) (imap.Mailbox, error) {
	s.user.mu.Lock()
	defer s.user.mu.Unlock()

	m := s.user.mailboxes[string(name)]
	if m == nil {
		return nil, fmt.Errorf("MemoryStore: unknown mailbox %s", name)
	}
	return m, nil
}

func (s *memorySession) CreateMailbox(n []byte, attrs imap.ListAttrFlag) error {
	s.user.mu.Lock()
	defer s.user.mu.Unlock()

	name := string(n)
	if s.user.mailboxes[name] != nil {
		return errors.New("memory session: mailbox exists")
	}
	s.user.mailboxes[name] = &memoryMailbox{
		server:    s.server,
		user:      s.user,
		name:      name,
		attrs:     attrs,
		uidnext:   1,
		mailboxID: s.user.nextMailboxID,
	}
	s.user.nextMailboxID++
	return nil
}

func (s *memorySession) DeleteMailbox(n []byte) error {
	s.user.mu.Lock()
	defer s.user.mu.Unlock()
	m := s.user.mailboxes[string(n)]
	if m == nil {
		return errors.New("memory session: mailbox does not exist")
	}
	for _, msg := range m.msgs {
		msg.emailMsg.Close()
	}
	delete(s.user.mailboxes, string(n))
	return nil
}

func (s *memorySession) RenameMailbox(oldName, newName []byte) error {
	s.user.mu.Lock()
	defer s.user.mu.Unlock()
	old, new := string(oldName), string(newName)

	m := s.user.mailboxes[old]
	if m == nil {
		return errors.New("MemoryStore: source mailbox does not exist")
	}
	if s.user.mailboxes[new] != nil {
		return errors.New("MemoryStore: destination mailbox exists")
	}
	delete(s.user.mailboxes, old)
	m.name = new
	m.uidValidity = s.user.uidValidityNext
	s.user.uidValidityNext++
	s.user.mailboxes[new] = m
	return nil
}

func (s *memorySession) RegisterPushDevice(mailbox string, device imapparser.ApplePushDevice) error {
	return nil
}

func (s *memorySession) Close() {
}

type memoryMailbox struct {
	server    *MemoryStore
	user      *memoryUser
	mailboxID int64

	mu          sync.Mutex
	name        string
	attrs       imap.ListAttrFlag
	msgs        []memoryMsg
	uidnext     uint32
	uidValidity uint32
}

func (m *memoryMailbox) ID() int64 {
	return m.mailboxID
}

func (m *memoryMailbox) Info() (imap.MailboxInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	info := imap.MailboxInfo{
		Summary: imap.MailboxSummary{
			Name:  m.name,
			Attrs: m.attrs,
		},
		NumMessages: uint32(len(m.msgs)),
		UIDNext:     m.uidnext,
		UIDValidity: m.uidValidity,
	}
	for i, m := range m.msgs {
		unseen := true
		hasRecent := false
		for _, flag := range m.emailMsg.Flags {
			switch flag {
			case `\Recent`:
				hasRecent = true
			case `\Seen`:
				unseen = false
			}
		}
		if unseen && info.FirstUnseenSeqNum == 0 {
			info.FirstUnseenSeqNum = uint32(i + 1)
		}
		if unseen {
			info.NumUnseen++
		}
		if hasRecent {
			info.NumRecent++
		}
		if m.summary.ModSeq > info.HighestModSequence {
			info.HighestModSequence = m.summary.ModSeq
		}
	}
	return info, nil
}

func (m *memoryMailbox) Append(flags [][]byte, date time.Time, data *iox.BufferFile) (uint32, error) {
	msg := memoryMsg{}

	m.user.mu.Lock()
	msg.summary.ModSeq = m.user.modSequenceNext
	m.user.modSequenceNext++
	m.user.mu.Unlock()

	var err error
	msg.emailMsg, err = msgcleaver.Cleave(m.server.Filer, data)
	if err != nil {
		return 0, fmt.Errorf("Memory.Append: %v", err)
	}
	msg.emailMsg.Date = date

	for _, flag := range flags {
		if string(flag) == `\Recent` {
			continue
		}
		msg.emailMsg.Flags = append(msg.emailMsg.Flags, string(flag))
	}
	sort.Strings(msg.emailMsg.Flags)

	m.mu.Lock()
	msg.summary.SeqNum = uint32(len(m.msgs) + 1)
	msg.summary.UID = m.uidnext
	m.uidnext++
	m.msgs = append(m.msgs, msg)
	m.mu.Unlock()

	return msg.summary.UID, nil
}

func (m *memoryMailbox) Search(op *imapparser.SearchOp, fn func(imap.MessageSummary)) error {
	matcher, err := imapparser.NewMatcher(op)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.msgs {
		msg := &m.msgs[i]
		if matcher.Match(msg) {
			fn(msg.summary)
		}
	}
	return nil
}

func (m *memoryMailbox) Fetch(uid bool, seqs []imapparser.SeqRange, changedSince int64, fn func(imap.Message)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.msgs {
		msg := &m.msgs[i]
		id := msg.summary.SeqNum
		if uid {
			id = msg.summary.UID
		}
		if !imapparser.SeqContains(seqs, id) {
			continue
		}
		if changedSince >= msg.summary.ModSeq {
			continue
		}
		// Copy emailMsg
		emailMsg := *msg.emailMsg
		emailMsg.Flags = append([]string{}, emailMsg.Flags...)
		emailMsg.Parts = append([]email.Part{}, emailMsg.Parts...)
		emailMsg.Headers = email.Header{}
		for _, entry := range msg.emailMsg.Headers.Entries {
			emailMsg.Headers.Add(entry.Key, append([]byte{}, entry.Value...))
		}
		for i := range emailMsg.Parts {
			// Emulate content-less loading to stress LoadPart.
			emailMsg.Parts[i].Content = nil
		}
		emailMsg.MailboxID = m.mailboxID

		retMsg := &memoryMessage{
			filer:        m.server.Filer,
			origEmailMsg: msg.emailMsg,
			emailMsg:     emailMsg,
			summary:      msg.summary,
		}
		fn(retMsg)
		emailMsg.Close()
	}
	return nil
}

func (m *memoryMailbox) Expunge(uidSeqs []imapparser.SeqRange, fn func(seqNum uint32)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	i := 0
	delta := uint32(0)
	for i < len(m.msgs) {
		msg := &m.msgs[i]
		msg.summary.SeqNum -= delta
		if uidSeqs != nil && !imapparser.SeqContains(uidSeqs, msg.summary.UID) {
			i++
			continue
		}
		if hasFlag(msg.emailMsg.Flags, `\Deleted`) {
			seqNum := msg.summary.SeqNum
			msg.emailMsg.Close()
			m.msgs = append(m.msgs[:i], m.msgs[i+1:]...)
			if fn != nil {
				fn(seqNum)
			}
			delta++
		} else {
			i++
		}
	}

	return nil
}

func (m *memoryMailbox) HighestModSequence() (modSeq int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, msg := range m.msgs {
		if msg.summary.ModSeq > modSeq {
			modSeq = msg.summary.ModSeq
		}
	}
	return modSeq, nil
}

func (m *memoryMailbox) Store(uid bool, seqs []imapparser.SeqRange, store *imapparser.Store) (res imap.StoreResults, err error) {
	var flags []string
	for _, f := range store.Flags {
		flags = append(flags, string(f))
	}
	var flagset map[string]bool
	if store.Mode == imapparser.StoreRemove {
		flagset = make(map[string]bool)
		for _, f := range flags {
			flagset[f] = true
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.msgs {
		msg := &m.msgs[i]
		id := msg.summary.SeqNum
		if uid {
			id = msg.summary.UID
		}
		if !imapparser.SeqContains(seqs, id) {
			continue
		}
		changed := false
		switch store.Mode {
		case imapparser.StoreAdd:
			for _, flag := range flags {
				if !hasFlag(msg.emailMsg.Flags, flag) {
					changed = true
					msg.emailMsg.Flags = append(msg.emailMsg.Flags, flag)
				}
			}
			sort.Strings(msg.emailMsg.Flags)
		case imapparser.StoreRemove:
			var newFlags []string
			for _, flag := range msg.emailMsg.Flags {
				if !flagset[flag] {
					changed = true
					newFlags = append(newFlags, flag)
				}
			}
			msg.emailMsg.Flags = newFlags
		case imapparser.StoreReplace:
			if store.UnchangedSince != 0 && msg.summary.ModSeq > store.UnchangedSince {
				res.FailedModified = imapparser.AppendSeqRange(res.FailedModified, id)
				continue
			}
			recent := hasFlag(msg.emailMsg.Flags, `\Recent`)
			changed = !reflect.DeepEqual(msg.emailMsg.Flags, flags)
			msg.emailMsg.Flags = append(msg.emailMsg.Flags[:0], flags...)
			if recent {
				msg.emailMsg.Flags = append(msg.emailMsg.Flags, `\Recent`)
			}
			sort.Strings(msg.emailMsg.Flags)
		}

		if !changed {
			if store.UnchangedSince != 0 && msg.summary.ModSeq > store.UnchangedSince {
				res.Stored = append(res.Stored, imap.StoreResult{
					Flags:       msg.emailMsg.Flags,
					ModSequence: msg.summary.ModSeq,
					SeqNum:      msg.summary.SeqNum,
					UID:         msg.summary.UID,
				})
			}
			continue
		}

		m.user.mu.Lock()
		newModSeq := m.user.modSequenceNext
		m.user.modSequenceNext++
		m.user.mu.Unlock()

		msg.summary.ModSeq = newModSeq

		res.Stored = append(res.Stored, imap.StoreResult{
			Flags:       msg.emailMsg.Flags,
			ModSequence: msg.summary.ModSeq,
			SeqNum:      msg.summary.SeqNum,
			UID:         msg.summary.UID,
		})
	}
	return res, nil
}

func (m *memoryMailbox) Move(uid bool, seqs []imapparser.SeqRange, dstMbox imap.Mailbox, fn func(seqNum, srcUID, dstUID uint32)) error {
	dst := dstMbox.(*memoryMailbox)
	if dst == m {
		return fmt.Errorf("memory.Move: moving to ourself. TODO is this an error?") // TODO
	}

	m.mu.Lock()
	dst.mu.Lock()
	defer m.mu.Unlock()
	defer dst.mu.Unlock()

	i := 0
	seqDelta := uint32(0)
	for i < len(m.msgs) {
		msg := &m.msgs[i]
		msg.summary.SeqNum -= seqDelta
		id := msg.summary.SeqNum
		if uid {
			id = msg.summary.UID
		}
		if !imapparser.SeqContains(seqs, id) {
			i++
			continue
		}
		seqDelta++

		dst.msgs = append(dst.msgs, *msg)
		msg = &dst.msgs[len(dst.msgs)-1]
		m.msgs = append(m.msgs[:i], m.msgs[i+1:]...)

		uid := dst.uidnext
		dst.uidnext++

		if fn != nil {
			fn(msg.summary.SeqNum, msg.summary.UID, uid)
		}

		msg.emailMsg.MailboxID = dst.mailboxID
		msg.summary.UID = uid
		msg.summary.SeqNum = uint32(len(dst.msgs))
	}

	return nil
}

func (m *memoryMailbox) Copy(uid bool, seqs []imapparser.SeqRange, dstMbox imap.Mailbox, fn func(srcUID, dstUID uint32)) error {
	dst := dstMbox.(*memoryMailbox)
	if dst == m {
		return fmt.Errorf("memory.Copy: copying to ourself. TODO is this an error?") // TODO
	}

	m.mu.Lock()
	dst.mu.Lock()
	defer m.mu.Unlock()
	defer dst.mu.Unlock()

	for i := 0; i < len(m.msgs); i++ {
		msg := m.msgs[i]

		id := msg.summary.SeqNum
		if uid {
			id = msg.summary.UID
		}
		if !imapparser.SeqContains(seqs, id) {
			continue
		}

		uid := dst.uidnext
		dst.uidnext++

		if fn != nil {
			fn(msg.summary.UID, uid)
		}

		emailMsg := *msg.emailMsg
		emailMsg.MailboxID = dst.mailboxID
		msg.emailMsg = &emailMsg
		msg.summary.UID = uid
		msg.summary.SeqNum = uint32(len(dst.msgs) + 1)
		dst.msgs = append(dst.msgs, msg)
	}

	return nil
}

func (m *memoryMailbox) Close() error {
	return nil
}

func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

type memoryMessage struct {
	filer        *iox.Filer
	summary      imap.MessageSummary
	emailMsg     email.Msg
	origEmailMsg *email.Msg
}

func (msg *memoryMessage) Summary() imap.MessageSummary { return msg.summary }

func (msg *memoryMessage) Msg() *email.Msg { return &msg.emailMsg }

func (msg *memoryMessage) LoadPart(partNum int) error {
	src := msg.origEmailMsg.Parts[partNum].Content
	if _, err := src.Seek(0, 0); err != nil {
		return err
	}
	dst := msg.filer.BufferFile(0)
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if _, err := dst.Seek(0, 0); err != nil {
		return err
	}
	msg.emailMsg.Parts[partNum].Content = dst
	return nil
}

func (msg *memoryMessage) SetSeen() error {
	if hasFlag(msg.emailMsg.Flags, `\Seen`) {
		return fmt.Errorf(`message %d already \Seen`, msg.summary.SeqNum)
	}
	msg.emailMsg.Flags = append(msg.emailMsg.Flags, `\Seen`)
	sort.Strings(msg.emailMsg.Flags)
	msg.origEmailMsg.Flags = append(msg.origEmailMsg.Flags, `\Seen`)
	sort.Strings(msg.origEmailMsg.Flags)
	return nil
}

type memoryMsg struct {
	summary  imap.MessageSummary
	emailMsg *email.Msg
}

// Methods implementing imapparser.MatchMessage.

func (msg *memoryMsg) UID() uint32     { return msg.summary.UID }
func (msg *memoryMsg) SeqNum() uint32  { return msg.summary.SeqNum }
func (msg *memoryMsg) ModSeq() int64   { return msg.summary.ModSeq }
func (msg *memoryMsg) Date() time.Time { return msg.emailMsg.Date }
func (msg *memoryMsg) Flag(name string) bool {
	for _, flag := range msg.emailMsg.Flags {
		if flag == name {
			return true
		}
	}
	return false
}
func (m *memoryMsg) Header(name string) string {
	key := email.CanonicalKey([]byte(name))
	return string(m.emailMsg.Headers.Get(key))
}
func (msg *memoryMsg) RFC822Size() int64 {
	return msg.emailMsg.EncodedSize
}
