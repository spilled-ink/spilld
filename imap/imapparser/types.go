// Package imapparser implements an IMAP command parser.
//
// It parses client commands for a server.
// At its core it implements the grammar from RFC 3501, along with
// the grammar for several extensions.
//
// See RFC 4466 for the grammar for many typical IMAP extensions.
package imapparser

import (
	"time"

	"crawshaw.io/iox"
)

type Command struct {
	Tag  []byte
	Name string

	// UID means the command response will report UIDs instead of SeqNums.
	// Name is one of: COPY, FETCH, SEARCH, STORE.
	UID bool

	// Name is one of:
	//	SELECT, EXAMINE, SUBSCRIBE, UNSUBSCRIBE, DELETE,
	//	STATUS, APPEND, COPY
	Mailbox []byte

	// Name is one of: SELECT, EXAMINE
	Condstore bool
	Qresync   QresyncParam

	// Name is one of: FETCH, STORE, COPY
	Sequences []SeqRange

	// Name is one of: APPEND, STORE
	Literal *iox.BufferFile

	Rename struct { // Name: RENAME
		OldMailbox []byte
		NewMailbox []byte
	}

	Params [][]byte // Name: ENABLE, ID

	Auth struct { // Name: LOGIN, AUTHENTICATE PLAIN
		Username []byte
		Password []byte
	}

	List List // Name is one of: LIST, LSUB

	Status struct { // Name: STATUS
		Items []StatusItem
	}

	Append struct { // Name: APPEND
		Flags [][]byte
		Date  []byte
	}

	FetchItems   []FetchItem // Name: FETCH
	ChangedSince int64       // Name: FETCH
	Vanished     bool        // Name: FETCH

	Store Store // Name: STORE

	Search Search // Name: SEARCH

	ApplePushService *ApplePushService // Name: XAPPLEPUSHSERVICE
}

type List struct {
	ReferenceName []byte
	MailboxGlob   []byte

	// RFC 5258 LIST-EXTENDED fields
	SelectOptions []string // SUBSCRIBED, REMOTE, RECURSIVEMATCH, SPECIAL-USE
	ReturnOptions []string // SUBSCRIBED, CHILDREN, SPECIAL-USE
}

type QresyncParam struct {
	UIDValidity      uint32
	ModSeq           int64
	UIDs             []SeqRange
	KnownSeqNumMatch []SeqRange
	KnownUIDMatch    []SeqRange
}

type Store struct {
	Mode           StoreMode
	Silent         bool
	Flags          [][]byte
	UnchangedSince int64
}

type ApplePushService struct {
	Mailboxes []string
	Version   int
	Subtopic  string
	Device    ApplePushDevice
}

type ApplePushDevice struct {
	AccountID   string
	DeviceToken string // hex-encoded
}

type StoreMode int

const (
	StoreUnknown StoreMode = iota
	StoreAdd               // +FLAGS
	StoreRemove            // -FLAGS
	StoreReplace           //  FLAGS
)

type StatusItem int

const (
	StatusUnknownItem StatusItem = iota
	StatusMessages
	StatusRecent
	StatusUIDNext
	StatusUIDValidity
	StatusUnseen
	StatusHighestModSeq
)

// SeqRange is a normalized IMAP seq-range.
// Normalized means that Min is always less than or equal to Max.
//
// The value 0 is a placeholder for '*'.
// When Min == Max, a SeqRange refers to a single value.
type SeqRange struct {
	Min uint32
	Max uint32
}

type FetchItem struct {
	Type    FetchItemType
	Peek    bool             // BODY.PEEK
	Section FetchItemSection // Type is FetchBody
	Partial struct {
		Start  uint32
		Length uint32
	}
}

type FetchItemSection struct {
	Path    []uint16
	Name    string // One of: HEADER, HEADER.FIELDS[.NOT], TEXT, MIME
	Headers [][]byte
}

type FetchItemType string

const (
	FetchUnknown = FetchItemType("FetchUnknown")

	FetchAll  = FetchItemType("ALL") // macro items, only fetch item in list
	FetchFull = FetchItemType("FULL")
	FetchFast = FetchItemType("FAST")

	FetchEnvelope      = FetchItemType("ENVELOPE")
	FetchFlags         = FetchItemType("FLAGS")
	FetchInternalDate  = FetchItemType("INTERNALDATE")
	FetchRFC822Header  = FetchItemType("RFC822.HEADER")
	FetchRFC822Size    = FetchItemType("RFC822.SIZE")
	FetchRFC822Text    = FetchItemType("RFC822.TEXT")
	FetchUID           = FetchItemType("UID")
	FetchBodyStructure = FetchItemType("BODYSTRUCTURE")
	FetchBody          = FetchItemType("BODY")
	FetchModSeq        = FetchItemType("MODSEQ")
)

type Search struct {
	Op      *SearchOp
	Charset string
	Return  []string // MIN, MAX, ALL, COUNT
}

type SearchOp struct {
	// Key is an IMAP search key.
	//
	// Two extra keys are defined that are not found in RFC 3501:
	//
	//	- AND: every element of Children must match
	//	  It is prettier than the grammar '('.
	//	  This allows the entire search command to be a SearchOp.
	//
	//	- SEQSET: the search op is a match against sequence IDs
	//	  This is a name for the implicit <sequence-set> grammar.
	//
	Key SearchKey

	// Children is set when Key is one of: AND, OR, NOT
	// For NOT, len(Children) == 1.
	Children []SearchOp

	// Value is set when Key is one of:
	//	BCC, CC, FROM,
	//      HEADER ("<field-name>: <string>"),
	//	KEYWORD, SUBJECT, TEXT, TO
	Value string

	Num       int64      // Key is one of: LARGER (uint32), SMALLER (uint32), MODSEQ
	Sequences []SeqRange // Key is one of: SEQSET, UID, UNDRAFT

	Date time.Time // Key is one of: BEFORE, ON, SENTBEFORE, SENTON, SENTSINCE, SINCE
}

type SearchKey string

type Mode int

const (
	ModeNonAuth Mode = iota
	ModeAuth
	ModeSelected
)
