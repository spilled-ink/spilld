// Package email is a light-weight set of types fundamental to processing email.
package email

import (
	"fmt"
	"io"
	"time"
)

// MsgID is a unique identifier for a message.
//
// MsgID is unique across all mailboxes.
//
// A message does not have a MsgID until it is stored in the client database.
type MsgID int64

func (id MsgID) String() string { return fmt.Sprintf("m%d", int64(id)) }

// Msg is an email message.
type Msg struct {
	MsgID       MsgID // assigned on insertion into user mailbox, 0 otherwise
	Seed        int64 // random used to seed multipart boundaries
	MailboxID   int64 // assigned on insertion into user mailbox, 0 otherwise
	RawHash     string
	Date        time.Time // TODO: raw user Date, sanatized Date, or server recv date?
	Headers     Header
	Flags       []string
	Parts       []Part // Parts[i].PartNum == i
	EncodedSize int64  // size of encoded message, IMAP value RFC822.SIZE
}

func (m *Msg) Close() {
	for _, p := range m.Parts {
		if p.Content != nil {
			p.Content.Close()
			p.Content = nil
		}
	}
}

// Part represents a single part of a MIME multipart message.
// A Msg with a single text/plain part is not multipart encoded.
type Part struct {
	PartNum        int
	Name           string
	IsBody         bool
	IsAttachment   bool
	IsCompressed   bool  // stored compressed on disk
	CompressedSize int64 // size of content when compressed if known
	ContentType    string
	ContentID      string
	Content        Buffer // uncompressed data
	BlobID         int64

	// TODO remove Path?
	Path                    string // MIME path as used in IMAP, ex. "1.2.3"
	ContentTransferEncoding string // "", "quoted-printable", "base64"
	ContentTransferSize     int64  // transfer-encoded size
	ContentTransferLines    int64  // transfer-encoded line count
}

// Buffer is content store.
//
// It is usually an *iox.BufferFile or *sqlite.Blob.
//
// Expect it to be fixed size.
// TODO: remove io.Writer?
// TODO: add io.ReaderAt?
type Buffer interface {
	io.Reader
	io.Writer
	io.Seeker
	io.Closer
	Size() int64
}
