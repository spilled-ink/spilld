// Package imap defines the core types used by the IMAP server.
//
// TODO document
// TODO remove iox dependency
// TODO remove imapparser dependency?
package imap

import (
	"sort"
	"time"

	"crawshaw.io/iox"
	"spilled.ink/email"
	"spilled.ink/imap/imapparser"
)

type Session interface {
	Mailboxes() ([]MailboxSummary, error)
	Mailbox(name []byte) (Mailbox, error)
	CreateMailbox(name []byte, attr ListAttrFlag) error
	DeleteMailbox(name []byte) error
	RenameMailbox(old, new []byte) error
	RegisterPushDevice(name string, device imapparser.ApplePushDevice) error
	Close()
}

type Mailbox interface {
	ID() int64

	Info() (MailboxInfo, error)

	// TODO: switch to io.ReadSeeker
	Append(flags [][]byte, date time.Time, data *iox.BufferFile) (uid uint32, err error)

	// Search finds all messages that match op and calls fn for each one.
	Search(op *imapparser.SearchOp, fn func(MessageSummary)) error

	// Fetch fetches the messages named by seqs and calls fn for each one.
	//
	// If uid is true then seqs is a set of UIDs, otherwise
	// it is a set of sequence numbers
	//
	// The Message passed to fn may have a nil Content for all parts.
	// If the imapserver needs the content it will call LoadPart.
	//
	// The Message is only valid for the duration of the call to fn.
	//
	// Fetch must Close the email.Msg after fn returns.
	Fetch(uid bool, seqs []imapparser.SeqRange, changedSince int64, fn func(Message)) error

	// Expunge deleted all messages in the mailbox with the \Deleted flag.
	//
	// If uidSeqs is non-nil then only messages whose UID matches and
	// have the \Deleted flag are expunged.
	//
	// If fn is non-nil it is called with the seqNum for each deleted
	// message. The sequence numbers follow the amazing rules of the IMAP
	// expunge command, that is, each is reported after the previous
	// is removed and the sequence numbers recalculated.
	Expunge(uidSeqs []imapparser.SeqRange, fn func(seqNum uint32)) error

	Store(uid bool, seqs []imapparser.SeqRange, store *imapparser.Store) (StoreResults, error)

	Move(uid bool, seqs []imapparser.SeqRange, dst Mailbox, fn func(seqNum, srcUID, dstUID uint32)) error

	Copy(uid bool, seqs []imapparser.SeqRange, dst Mailbox, fn func(srcUID, dstUID uint32)) error

	HighestModSequence() (int64, error) // TODO: just use Info?

	Close() error
}

type MailboxSummary struct {
	Name  string
	Attrs ListAttrFlag
}

type MailboxInfo struct {
	Summary MailboxSummary
	// TODO Flags
	NumMessages        uint32
	NumRecent          uint32
	NumUnseen          uint32
	UIDNext            uint32
	UIDValidity        uint32
	FirstUnseenSeqNum  uint32
	HighestModSequence int64
}

type StoreResult struct {
	SeqNum      uint32
	UID         uint32
	Flags       []string
	ModSequence int64
}

type StoreResults struct {
	Stored         []StoreResult
	FailedModified []imapparser.SeqRange
}

// TODO type Seqs struct { UID bool; Seqs []imapparser.SeqRange } ?

type MessageSummary struct {
	SeqNum uint32
	UID    uint32
	ModSeq int64
}

type Message interface {
	Summary() MessageSummary

	// Msg returns the email.Msg.
	// Subsequent calls to Msg return the same memory.
	Msg() *email.Msg

	// TODO: conditional LoadPartsSummary in Fetch.
	// Reduces number of SQL queries from O(n) to O(1) in easy cases.

	// LoadPart loads Msg().Part[partNum].Content.
	//
	// Any subsequent calls to Msg will return the part with content
	// as long as Message is valid.
	LoadPart(partNum int) error

	// SetSeen sets the \Seen flag on this message.
	SetSeen() error
}

type Notifier interface {
	Notify(userID int64, mailboxID int64, mailboxName string, devices []imapparser.ApplePushDevice)
}

type ListAttrFlag int

const (
	AttrNone        ListAttrFlag = 0
	AttrNoinferiors ListAttrFlag = 1 << iota
	AttrNoselect
	AttrMarked
	AttrUnmarked

	// SPECIAL-USE mailbox attributes, RFC 6164
	AttrAll
	AttrArchive
	AttrDrafts
	AttrFlagged
	AttrJunk
	AttrSent
	AttrTrash
)

func (attrs ListAttrFlag) String() (res string) {
	for _, attr := range attrList {
		if attrs&attr != 0 {
			s := attrStrings[attr]
			if res == "" {
				res = s
			} else {
				res = res + " " + s
			}
		}
	}
	return res
}

var attrStrings = map[ListAttrFlag]string{
	AttrNoinferiors: `\Noinferiors`,
	AttrNoselect:    `\Noselect`,
	AttrMarked:      `\Marked`,
	AttrUnmarked:    `\Unmarked`,
	AttrAll:         `\All`,
	AttrArchive:     `\Archive`,
	AttrDrafts:      `\Drafts`,
	AttrFlagged:     `\Flagged`,
	AttrJunk:        `\Junk`,
	AttrSent:        `\Sent`,
	AttrTrash:       `\Trash`,
}

var attrList = func() (attrList []ListAttrFlag) {
	for attr := range attrStrings {
		attrList = append(attrList, attr)
	}
	sort.Slice(attrList, func(i, j int) bool { return attrList[i] < attrList[j] })
	return attrList
}()
