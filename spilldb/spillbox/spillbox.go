// Package spillbox manages a single user mailbox.
package spillbox

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/email"
	"spilled.ink/email/msgbuilder"
	"spilled.ink/imap"
	"spilled.ink/imap/imapparser"
	"spilled.ink/spilldb/spillbox/prettyhtml"
	"spilled.ink/third_party/imf"
)

type ConvoID int64

type ContactID int64

type AddressID int64

func parseID(prefix, str string) (int64, error) {
	if !strings.HasPrefix(str, prefix) || len(str) < len(prefix)+1 {
		return 0, fmt.Errorf("bad prefix: %q", str)
	}
	i, err := strconv.ParseInt(str[len(prefix):], 10, 64)
	if err != nil {
		return 0, err
	}
	if i < 0 {
		return 0, fmt.Errorf("%d is negative", i)
	}
	return i, nil
}

func ParseConvoID(str string) (ConvoID, error) {
	id, err := parseID("cvo", str)
	if err != nil {
		return 0, fmt.Errorf("ParseConvoID: %v", err)
	}
	return ConvoID(id), nil
}

func ParseMsgID(str string) (email.MsgID, error) {
	id, err := parseID("m", str)
	if err != nil {
		return 0, fmt.Errorf("ParseMsgID: %v", err)
	}
	return email.MsgID(id), nil
}

func ParseContactID(str string) (ContactID, error) {
	id, err := parseID("c", str)
	if err != nil {
		return 0, fmt.Errorf("ParseContactID: %v", err)
	}
	return ContactID(id), nil
}

func (id ConvoID) String() string   { return fmt.Sprintf("cvo%d", int64(id)) }
func (id ContactID) String() string { return fmt.Sprintf("c%d", int64(id)) }
func (id AddressID) String() string { return fmt.Sprintf("a%d", int64(id)) }

type MsgState int64

const (
	MsgReady    MsgState = 1
	MsgFetching MsgState = 3
	MsgExpunged MsgState = 7
)

func (s MsgState) String() string {
	switch s {
	case MsgReady:
		return "MsgReady"
	case MsgFetching:
		return "MsgFetching"
	case MsgExpunged:
		return "MsgExpunged"
	default:
		return fmt.Sprintf("MsgState(%d:Unknown)", int(s))
	}
}

type LabelID int64

func (id LabelID) String() string { return fmt.Sprintf("l%x", int64(id)) }

// UnixTime is a time object that is marshalled to JSON as integer seconds.
//
// Any sub-second precision or time zone information is meaningless and
// should not be relied on.
type UnixTime time.Time

func (t UnixTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).Unix())
}
func (t *UnixTime) UnmarshalJSON(data []byte) error {
	sec, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	*t = UnixTime(time.Unix(sec, 0))
	return nil
}

type AddressRole int8

const (
	RoleFrom    AddressRole = 1
	RoleTo      AddressRole = 2
	RoleCC      AddressRole = 3
	RoleBCC     AddressRole = 4
	RoleMention AddressRole = 5
)

func (r AddressRole) String() string {
	switch r {
	case RoleFrom:
		return "RoleFrom"
	case RoleTo:
		return "RoleTo"
	case RoleCC:
		return "RoleCC"
	case RoleBCC:
		return "RoleBCC"
	case RoleMention:
		return "RoleMention"
	default:
		return fmt.Sprintf("RoleUnknown(%d)", int(r))
	}
}

func (r AddressRole) Header() email.Key {
	switch r {
	case RoleFrom:
		return "From"
	case RoleTo:
		return "To"
	case RoleCC:
		return "CC"
	case RoleBCC:
		return "BCC"
	default:
		return ""
	}
}

func InsertRandID(stmt *sqlite.Stmt, param string) (id int64, err error) {
	return InsertRandIDMin(stmt, param, 1)
}

func InsertRandIDMin(stmt *sqlite.Stmt, param string, minVal int64) (id int64, err error) {
	return sqlitex.InsertRandID(stmt, param, minVal, 1<<23)
}

type Box struct {
	PoolRO *sqlitex.Pool
	PoolRW *sqlitex.Pool

	labelPersonalMail LabelID

	filer     *iox.Filer
	pretty    *prettyhtml.Prettifier
	notifiers []imap.Notifier
	userID    int64

	mu      sync.Mutex
	devices map[string][]imapparser.ApplePushDevice
}

type NewMsgFunc func(mailboxID int64, mailboxName string, msgID email.MsgID)

func New(userID int64, filer *iox.Filer, dbfile string, poolSize int) (_ *Box, err error) {
	box := &Box{
		userID: userID,
		filer:  filer,
	}
	defer func() {
		if err != nil {
			box.Close()
		}
	}()

	dbdir, dbfilename := filepath.Split(dbfile)
	blobsDBFile := filepath.Join(dbdir, strings.TrimSuffix(dbfilename, ".db")+"_blobs.db")

	flags := sqlite.SQLITE_OPEN_SHAREDCACHE |
		sqlite.SQLITE_OPEN_WAL |
		sqlite.SQLITE_OPEN_URI |
		sqlite.SQLITE_OPEN_NOMUTEX
	flagsRW := flags | sqlite.SQLITE_OPEN_READWRITE | sqlite.SQLITE_OPEN_CREATE

	box.PoolRW, err = sqlitex.Open(dbfile, flagsRW, 1)
	if err != nil {
		return nil, err
	}
	if err := attachBlobsDB(box.PoolRW, 1, blobsDBFile); err != nil {
		return nil, err
	}
	conn := box.PoolRW.Get(nil)
	err = initDB(conn)
	box.PoolRW.Put(conn)
	if err != nil {
		return nil, fmt.Errorf("spillbox.New: init DB: %v", err)
	}

	if poolSize > 1 {
		flagsRO := flags | sqlite.SQLITE_OPEN_READONLY
		box.PoolRO, err = sqlitex.Open(dbfile, flagsRO, poolSize-1)
		if err != nil {
			return nil, err
		}
		if err := attachBlobsDB(box.PoolRO, poolSize-1, blobsDBFile); err != nil {
			return nil, err
		}
	} else {
		box.PoolRO = box.PoolRW
	}

	box.devices = make(map[string][]imapparser.ApplePushDevice)
	conn = box.PoolRO.Get(nil)
	defer box.PoolRO.Put(conn)
	stmt := conn.Prep("SELECT Mailbox, AppleAccountID, AppleDeviceToken FROM ApplePushDevices;")
	for {
		if hasNext, err := stmt.Step(); err != nil {
			return nil, err
		} else if !hasNext {
			break
		}
		mailbox := stmt.GetText("Mailbox")
		box.devices[mailbox] = append(box.devices[mailbox], imapparser.ApplePushDevice{
			AccountID:   stmt.GetText("AppleAccountID"),
			DeviceToken: stmt.GetText("AppleDeviceToken"),
		})
	}

	return box, nil
}

func attachBlobsDB(pool *sqlitex.Pool, poolSize int, blobsDBFile string) error {
	var conns []*sqlite.Conn
	defer func() {
		for _, conn := range conns {
			pool.Put(conn)
		}
	}()

	for i := 0; i < poolSize; i++ {
		conn := pool.Get(nil)
		if conn == nil {
			return fmt.Errorf("spillbox: cannot get connection %d to attach blobs", i)
		}
		conns = append(conns, conn)

		stmt, _, err := conn.PrepareTransient("ATTACH DATABASE $db AS blobs;")
		if err != nil {
			return err
		}
		stmt.SetText("$db", blobsDBFile)
		_, err = stmt.Step()
		stmt.Finalize()
		if err != nil {
			return err
		}
	}

	return nil
}

func (box *Box) RegisterNotifier(notifier imap.Notifier) {
	box.notifiers = append(box.notifiers, notifier)
}

func (box *Box) Close() (err error) {
	if box == nil {
		return fmt.Errorf("spillbox: already closed")
	}
	if box.PoolRW != nil {
		err = box.PoolRW.Close()
	}
	if box.PoolRO != nil && box.PoolRW != box.PoolRO {
		if cerr := box.PoolRO.Close(); err == nil {
			err = cerr
		}
	}
	box.PoolRW = nil
	box.PoolRO = nil
	return err
}

func initDB(conn *sqlite.Conn) (err error) {
	stmt, _, err := conn.PrepareTransient("PRAGMA journal_mode=WAL;")
	if err != nil {
		return err
	}
	_, err = stmt.Step()
	stmt.Finalize()
	if err != nil {
		return err
	}

	defer sqlitex.Save(conn)(&err)
	if err := sqlitex.ExecScript(conn, createSQL); err != nil {
		return err
	}
	return nil
}

func (box *Box) Init(ctx context.Context) error {
	conn := box.PoolRW.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer box.PoolRW.Put(conn)

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
		if err := CreateMailbox(conn, mbox.name, mbox.attr); err != nil {
			return err
		}
	}
	return nil
}

func (box *Box) RegisterPushDevice(ctx context.Context, mailbox string, device imapparser.ApplePushDevice) error {
	conn := box.PoolRW.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer box.PoolRW.Put(conn)

	stmt := conn.Prep("SELECT count(*) FROM ApplePushDevices WHERE Mailbox=$mailbox AND AppleAccountID=$appleAccountID AND AppleDeviceToken=$appleDeviceToken;")
	stmt.SetText("$mailbox", mailbox)
	stmt.SetText("$appleAccountID", device.AccountID)
	stmt.SetText("$appleDeviceToken", device.DeviceToken)
	count, err := sqlitex.ResultInt(stmt)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	stmt = conn.Prep("INSERT INTO ApplePushDevices (Mailbox, AppleAccountID, AppleDeviceToken) VALUES ($mailbox, $appleAccountID, $appleDeviceToken);")
	stmt.SetText("$mailbox", mailbox)
	stmt.SetText("$appleAccountID", device.AccountID)
	stmt.SetText("$appleDeviceToken", device.DeviceToken)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	box.mu.Lock()
	box.devices[mailbox] = append(box.devices[mailbox], device)
	box.mu.Unlock()

	return nil
}

func extractMsgID(hash string) int64 {
	msgID := int32(binary.BigEndian.Uint64([]byte(hash)))
	if msgID < 0 {
		msgID = -msgID
	}
	if msgID == 0 {
		msgID = 1
	}
	return int64(msgID)
}

func insertDeliveredTo(conn *sqlite.Conn, deliveredTo []byte) (err error) {
	stmt := conn.Prep("SELECT count(*) FROM Addresses WHERE Address = $addr;")
	stmt.SetBytes("$addr", deliveredTo)
	found, err := sqlitex.ResultInt(stmt)
	if err != nil {
		return err
	}
	if found != 0 {
		return nil
	}
	norm := normalizeAddr(deliveredTo)
	stmt.SetBytes("$addr", norm)
	found, err = sqlitex.ResultInt(stmt)
	if err != nil {
		return err
	}
	if found != 0 {
		return nil
	}

	stmt = conn.Prep(`INSERT INTO Addresses (AddressID, ContactID, Address, DefaultAddr, Visible)
		VALUES ($addressID, 1, $addr, FALSE, TRUE);`)
	stmt.SetBytes("$addr", deliveredTo)
	if _, err := InsertRandID(stmt, "$addressID"); err != nil {
		return err
	}
	stmt.Reset()

	if !bytes.Equal(norm, deliveredTo) {
		stmt.SetBytes("$addr", norm)
		if _, err := InsertRandID(stmt, "$addressID"); err != nil {
			return err
		}
		stmt.Reset()
	}

	return nil
}

// TODO: these errors shouldn't stop mail delivery.
func InsertAddresses(conn *sqlite.Conn, msgID email.MsgID, hdr email.Header) (err error) {
	defer sqlitex.Save(conn)(&err)

	// The header Delivered-To: gives us a synonym for ourselves.
	// TODO: verify these headers are from reliable sources using DKIM
	for _, deliveredTo := range hdr.Index["Delivered-To"] {
		if err := insertDeliveredTo(conn, deliveredTo); err != nil {
			return err
		}
	}

	stmt := conn.Prep("INSERT INTO MsgAddresses (MsgID, AddressID, Role) VALUES ($msgID, $addrID, $role);")

	var fromID AddressID
	if from := string(hdr.Get("From")); from != "" {
		fromAddr, err := imf.ParseAddress(from)
		if err != nil {
			return fmt.Errorf("InsertAddresses: %v: parsing From header: %v", msgID, err)
		}
		// TODO: check ContactID, check this is us.
		fromID, _, err = ResolveAddressID(conn, fromAddr, true)
		if err != nil {
			return fmt.Errorf("InsertAddresses: %v: resolving From addr: %v", msgID, err)
		}
	}
	stmt.SetInt64("$msgID", int64(msgID))
	stmt.SetInt64("$addrID", int64(fromID))
	stmt.SetInt64("$role", int64(RoleFrom))
	if _, err := stmt.Step(); err != nil {
		return err
	}

	roles := []AddressRole{RoleTo, RoleCC, RoleBCC}
	for _, role := range roles {
		str := strings.TrimSpace(string(hdr.Get(role.Header())))
		if str == "" {
			continue
		}
		addrs, err := imf.ParseAddressList(str)
		if err != nil {
			return fmt.Errorf("InsertAddresses: %v: parsing %s addr %q: %v", msgID, role, str, err)
		}
		for _, addr := range addrs {
			id, _, err := ResolveAddressID(conn, addr, true)
			if err != nil {
				return fmt.Errorf("InsertAddresses: %v: resolving %s addr: %v", msgID, role, err)
			}
			stmt.Reset()
			stmt.SetInt64("$addrID", int64(id))
			stmt.SetInt64("$role", int64(role))
			if _, err := stmt.Step(); err != nil {
				return err
			}
		}
	}

	return nil
}

var (
	noreplyRE       = regexp.MustCompile(`(?i)no.?.?reply.*@`)
	noreplyDomainRE = regexp.MustCompile(`(?i)@.*noreply`)
)

/*func (box *Box) updateSearch(ctx context.Context) error {
	conn := box.PoolRW.Get(ctx)
	if conn == nil {
		return context.Canceled
	}
	defer box.PoolRW.Put(conn)

	// TODO: fill in the body from a blob
	stmt := conn.Prep(`INSERT INTO MsgSearch (MsgID, ConvoID, Body)
		SELECT MsgID, ConvoID, "" as Body FROM Msgs
		WHERE MsgID IN (
			SELECT MsgID FROM Msgs EXCEPT SELECT MsgID FROM MsgSearch
		);`)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	return nil
}*/

func findLabel(conn *sqlite.Conn, labelName string) (LabelID, error) {
	stmt := conn.Prep("SELECT LabelID from Labels WHERE Label = $labelName;")
	stmt.SetText("$labelName", labelName)
	id, err := sqlitex.ResultInt64(stmt)
	return LabelID(id), err
}

func LoadMsgHdrs(conn *sqlite.Conn, msgID email.MsgID) (*email.Header, error) {
	stmt := conn.Prep("SELECT HdrsAll FROM Msgs WHERE MsgID = $msgID;")
	stmt.SetInt64("$msgID", int64(msgID))
	hdrs, err := sqlitex.ResultText(stmt)
	if err != nil {
		return nil, err
	}
	hdr, err := imf.NewReader(bufio.NewReader(strings.NewReader(hdrs + "\n\n"))).ReadMIMEHeader()
	if err != nil {
		return nil, fmt.Errorf("%s: could not parse headers: %v", msgID, err)
	}
	return &hdr, nil
}

/*func MarkMsgRead(conn *sqlite.Conn, msgID email.MsgID) error {
	// TODO: set \Seen flag
	stmt := conn.Prep("UPDATE Msgs SET State = $msgRead WHERE MsgID = $msgID AND State = $msgUnread;")
	stmt.SetInt64("$msgID", int64(msgID))
	stmt.SetInt64("$msgRead", int64(MsgRead))
	stmt.SetInt64("$msgUnread", int64(MsgUnread))
	if _, err := stmt.Step(); err != nil {
		return err
	}
	return nil
}*/

type Contact struct {
	ContactID ContactID
	Name      string
}

type ConvoSummary struct {
	Contacts []Contact
}

// contentLinks map Content-ID -> local URL path for content
func contentLinks(conn *sqlite.Conn, msgID email.MsgID) (map[string]string, error) {
	stmt := conn.Prep(`SELECT ContentID, BlobID FROM MsgParts
		WHERE MsgID = $msgID AND IsBody = FALSE AND IsAttachment = FALSE;`)
	stmt.SetInt64("$msgID", int64(msgID))
	links := make(map[string]string)
	for {
		if hasNext, err := stmt.Step(); err != nil {
			return nil, err
		} else if !hasNext {
			break
		}
		contentID := stmt.GetText("ContentID")
		blobID := stmt.GetInt64("BlobID")

		links[contentID] = fmt.Sprintf("/attachment/%d", blobID)
	}
	return links, nil
}

type ContentState int

// Controls how the part content is loaded in a message
//
// Note that ContentDB opens all blobs on the same *sqlite.Conn
// and it is the responsibility of the caller to keep track of
// the connection and blob lifetimes.
const (
	ContentFullCopy ContentState = iota // copied into *iox.BufferFile
	ContentDB                           // *sqlite.Blob or a copy
	ContentNil                          // nil
)

// LoadMessage loads the message msgID from the database.
//
// It is the callers responsibility to close the message.
func LoadMessage(conn *sqlite.Conn, filer *iox.Filer, msgID email.MsgID, contentState ContentState) (msg *email.Msg, err error) {
	msg = new(email.Msg)
	msg.MsgID = msgID
	// TODO msg.Seed
	// TODO msg.RawHash
	// TODO msg.Date
	// TODO msg.Flags
	// TODO msg.EncodedSize

	stmt := conn.Prep("SELECT HdrsAll FROM Msgs WHERE MsgID = $msgID;")
	stmt.SetInt64("$msgID", int64(msgID))
	headers, err := sqlitex.ResultText(stmt)
	if err != nil {
		return nil, fmt.Errorf("spillbox.LoadMessage(%s): loading headers: %v", msgID, err)
	}
	msg.Headers, err = imf.NewReader(bufio.NewReader(strings.NewReader(headers))).ReadMIMEHeader()
	if err != nil {
		return nil, fmt.Errorf("spillbox.LoadMessage(%s): reading headers: %v", msgID, err)
	}

	// TODO: call LoadPartSummary
	stmt = conn.Prep(`SELECT
		PartNum, IsBody, IsAttachment, IsCompressed,
		ContentType, ContentID, Name, BlobID
		FROM MsgParts
		WHERE MsgID = $msgID ORDER BY PartNum;`)
	stmt.SetInt64("$msgID", int64(msgID))

	for {
		if hasNext, err := stmt.Step(); err != nil {
			msg.Close()
			return nil, fmt.Errorf("spillbox.LoadMessage(%s): enumerating parts: %v", msgID, err)
		} else if !hasNext {
			break
		}
		blobID := stmt.GetInt64("BlobID")
		isCompressed := stmt.GetInt64("IsCompressed") > 0
		p := email.Part{
			PartNum:      int(stmt.GetInt64("PartNum")),
			Name:         stmt.GetText("Name"),
			IsBody:       stmt.GetInt64("IsBody") != 0,
			IsAttachment: stmt.GetInt64("IsAttachment") != 0,
			IsCompressed: isCompressed,
			ContentType:  stmt.GetText("ContentType"),
			ContentID:    stmt.GetText("ContentID"),
		}
		p.Content, p.CompressedSize, err = readMsgPart(conn, filer, blobID, isCompressed, contentState)
		if err != nil {
			stmt.Reset()
			msg.Close()
			return nil, fmt.Errorf("spillbox.LoadMessage(%s): part %d: %v", msgID, p.PartNum, err)
		}
		msg.Parts = append(msg.Parts, p)
	}

	return msg, nil
}

func LoadPartsSummary(conn *sqlite.Conn, msgID email.MsgID) (parts []email.Part, err error) {
	stmt := conn.Prep(`SELECT
		PartNum, IsBody, IsAttachment, IsCompressed,
		ContentType, ContentID, Name, MsgParts.BlobID,
		ContentTransferEncoding, ContentTransferSize,
		ContentTransferLines,
		length(blobs.Blobs.Content) AS CompressedSize
		FROM MsgParts
		INNER JOIN blobs.Blobs ON blobs.Blobs.BlobID = MsgParts.BlobID
		WHERE MsgID = $msgID ORDER BY PartNum;`)
	stmt.SetInt64("$msgID", int64(msgID))

	for {
		if hasNext, err := stmt.Step(); err != nil {
			return nil, fmt.Errorf("spillbox.LoadPartSummary(%v): %v", msgID, err)
		} else if !hasNext {
			break
		}
		isCompressed := stmt.GetInt64("IsCompressed") > 0
		p := email.Part{
			PartNum:        int(stmt.GetInt64("PartNum")),
			Name:           stmt.GetText("Name"),
			IsBody:         stmt.GetInt64("IsBody") != 0,
			IsAttachment:   stmt.GetInt64("IsAttachment") != 0,
			IsCompressed:   isCompressed,
			CompressedSize: stmt.GetInt64("CompressedSize"),
			ContentType:    stmt.GetText("ContentType"),
			ContentID:      stmt.GetText("ContentID"),
			BlobID:         stmt.GetInt64("BlobID"),

			ContentTransferEncoding: stmt.GetText("ContentTransferEncoding"),
			ContentTransferSize:     stmt.GetInt64("ContentTransferSize"),
			ContentTransferLines:    stmt.GetInt64("ContentTransferLines"),
		}
		parts = append(parts, p)
	}

	return parts, nil
}

func BuildMessage(conn *sqlite.Conn, filer *iox.Filer, msgID email.MsgID) (*iox.BufferFile, error) {
	msg, err := LoadMessage(conn, filer, msgID, ContentDB)
	if err != nil {
		return nil, err
	}
	defer msg.Close()
	builder := msgbuilder.Builder{
		Filer: filer,
	}
	buf := filer.BufferFile(0)
	if err := builder.Build(buf, msg); err != nil {
		buf.Close()
		return nil, err
	}
	return buf, nil
}

const encodeBnd = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._"

var boundaryEnc = base64.NewEncoding(encodeBnd)

func randBoundary(seed int64) func() string {
	rnd := rand.New(rand.NewSource(seed))
	return func() string {
		var buf [12]byte
		_, err := io.ReadFull(rnd, buf[:])
		if err != nil {
			panic(err)
		}
		return "Spilled_Ink=" + boundaryEnc.EncodeToString(buf[:])
	}
}

func LoadPartContent(conn *sqlite.Conn, filer *iox.Filer, part *email.Part) error {
	buf, _, err := readMsgPart(conn, filer, part.BlobID, part.IsCompressed, ContentFullCopy)
	if err != nil {
		return fmt.Errorf("LoadPartContent(blobid=%d): %v", part.BlobID, err)
	}
	part.Content = buf
	if _, err := part.Content.Seek(0, 0); err != nil {
		return fmt.Errorf("LoadPartContent(blobid=%d): seek: %v", part.BlobID, err)
	}
	return nil
}

func readMsgPart(conn *sqlite.Conn, filer *iox.Filer, blobID int64, isCompressed bool, contentState ContentState) (_ email.Buffer, compressedSize int64, err error) {
	if contentState == ContentNil {
		stmt := conn.Prep("SELECT length(Content) FROM blobs.Blobs WHERE BlobID = $BlobID;")
		stmt.SetInt64("$BlobID", blobID)
		compressedSize, err = sqlitex.ResultInt64(stmt)
		return nil, compressedSize, err
	}

	var blob *sqlite.Blob
	blob, err = conn.OpenBlob("blobs", "Blobs", "Content", blobID, false)
	if err != nil {
		return nil, 0, err
	}
	if !isCompressed && contentState == ContentDB {
		return blob, 0, nil
	}

	dst := filer.BufferFile(0)
	defer func() {
		blob.Close()
		if err != nil {
			dst.Close()
		}
	}()

	if isCompressed {
		compressedSize = blob.Size()
		zr, err := gzip.NewReader(blob)
		if err != nil {
			return nil, 0, err
		}
		if _, err = io.Copy(dst, zr); err != nil {
			return nil, 0, err
		}
		if err := zr.Close(); err != nil {
			return nil, 0, err
		}
	} else {
		if _, err = io.Copy(dst, blob); err != nil {
			return nil, 0, err
		}
	}

	return dst, compressedSize, nil
}
