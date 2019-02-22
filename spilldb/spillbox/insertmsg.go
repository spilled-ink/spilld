package spillbox

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/email"
	"spilled.ink/imap/imapparser"
)

// InsertMsg inserts a message into the client database.
//
// TODO: do we want do this ? *********
// All fields of msg and all parts must be fully specified in the
// msg passed, and must not differ from any previous call to InsertMsg,
// with one exception: the content of parts may be nil.
//
// Subsequent calls to InsertMsg with content and the MsgID set will
// insert that content into the database.
//
// When content has been written for all parts, InsertMsg will report
// done as true.
// TODO: do we want do this ? *********
//
// On success, msg is filled out with a MsgID.
func (c *Box) InsertMsg(ctx context.Context, msg *email.Msg, stagingID int64) (done bool, err error) {
	conn := c.PoolRW.Get(ctx)
	if conn == nil {
		return false, context.Canceled
	}
	defer c.PoolRW.Put(conn)

	done, err = c.insertMsg(conn, msg, stagingID)
	if err != nil {
		return false, fmt.Errorf("InsertMsg: %v", err)
	}
	if !done {
		return done, nil
	}

	if stagingID != 0 && len(c.notifiers) > 0 {
		stmt := conn.Prep("SELECT Name from Mailboxes WHERE MailboxID = $mailboxID")
		stmt.SetInt64("$mailboxID", msg.MailboxID)
		mailboxName, err := sqlitex.ResultText(stmt)
		if err != nil {
			return false, fmt.Errorf("mailbox name: %v", err)
		}

		c.mu.Lock()
		devices := append([]imapparser.ApplePushDevice{}, c.devices[mailboxName]...)
		c.mu.Unlock()

		for i := 0; i < len(c.notifiers); i++ {
			go c.notifiers[i].Notify(c.userID, msg.MailboxID, mailboxName, devices)
		}
	}
	return true, nil
}

func (c *Box) insertMsg(conn *sqlite.Conn, msg *email.Msg, stagingID int64) (done bool, err error) {
	defer sqlitex.Save(conn)(&err)

	if msg.RawHash == "" {
		return false, errors.New("missing hash")
	}

	if msg.MsgID != 0 {
		// TODO: invert, SELECT RawHash.
		// Followup call. Check we have the right RawHash
		stmt := conn.Prep("SELECT MsgID FROM Msgs WHERE RawHash = $rawHash;")
		stmt.SetText("$rawHash", msg.RawHash)
		if hasNext, err := stmt.Step(); err != nil {
			return false, err

		} else if hasNext {
			msgID := email.MsgID(stmt.GetInt64("MsgID"))
			stmt.Reset()
			if msgID != msg.MsgID {
				return false, fmt.Errorf("raw hash for %v has changed", msgID)
			}
		}

	} else {
		hdrBuf := new(bytes.Buffer)
		if _, err := msg.Headers.Encode(hdrBuf); err != nil {
			return false, err
		}
		stmt := conn.Prep(`INSERT INTO blobs.Blobs (Sha256, Content) VALUES ($sha256, $hdrs);`)
		hashVal := sha256.Sum256(hdrBuf.Bytes())
		stmt.SetText("$sha256", hex.EncodeToString(hashVal[:]))
		stmt.SetZeroBlob("$hdrs", int64(hdrBuf.Len()))
		if _, err := stmt.Step(); err != nil {
			return false, err
		}
		hdrsBlobID := conn.LastInsertRowID()
		blob, err := conn.OpenBlob("blobs", "Blobs", "Content", hdrsBlobID, true)
		if err != nil {
			return false, err
		}
		_, err = blob.Write(hdrBuf.Bytes())
		if err2 := blob.Close(); err == nil {
			err = err2
		}
		if err != nil {
			return false, err
		}

		flagsBuf := new(bytes.Buffer)
		encodeFlags(flagsBuf, msg.Flags)

		stmt = conn.Prep(`INSERT INTO Msgs (
				MsgID, StagingID, Seed, RawHash, State,
				HdrsBlobID, Date, Flags, EncodedSize
			) VALUES (
				$msgID, $stagingID, $seed, $rawHash, $state,
				$hdrsBlobID, $date, $flags, $encodedSize
			);`)
		stmt.SetText("$rawHash", msg.RawHash)
		if stagingID != 0 {
			stmt.SetInt64("$stagingID", stagingID)
		} else {
			stmt.SetNull("$stagingID")
		}
		stmt.SetInt64("$seed", msg.Seed)
		stmt.SetInt64("$state", int64(MsgFetching))
		stmt.SetInt64("$hdrsBlobID", hdrsBlobID)
		stmt.SetInt64("$date", msg.Date.Unix())
		stmt.SetBytes("$flags", flagsBuf.Bytes())
		stmt.SetInt64("$encodedSize", msg.EncodedSize)
		// TODO stmt.SetInt64("$readyDate", msg.ReadyDate)
		//stmt.SetText("$parseError", msg.ParseError)
		msgID := extractMsgID(msg.RawHash)
		stmt.SetInt64("$msgID", msgID)
		_, err = stmt.Step()
		if sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_PRIMARYKEY {
			msgID, err = InsertRandID(stmt, "$msgID")
		}
		if err != nil {
			return false, err
		}
		msg.MsgID = email.MsgID(msgID)

		if err := InsertAddresses(conn, msg.MsgID, msg.Headers); err != nil {
			msg.MsgID = 0
			return false, err
		}
	}

	for i := range msg.Parts {
		part := &msg.Parts[i]
		if err := insertPart(conn, msg.MsgID, part); err != nil {
			msg.MsgID = 0
			return false, fmt.Errorf("part %d: %v", i, err)
		}
	}

	stmt := conn.Prep("SELECT count(*) FROM blobs.Blobs WHERE Content IS NULL AND BlobID IN (SELECT BlobID FROM MsgParts WHERE MsgID = $MsgID);")
	stmt.SetInt64("$MsgID", int64(msg.MsgID))
	if count, err := sqlitex.ResultInt(stmt); err != nil {
		return false, err
	} else if count > 0 {
		// More message parts need to be stored in subsequent InsertMsg calls.
		return false, nil
	}

	mailboxID, err := c.setMsgFetched(conn, msg.MsgID, msg.MailboxID)
	if err != nil {
		return false, err
	}
	msg.MailboxID = mailboxID
	return true, nil
}

func countMsgs(conn *sqlite.Conn, mailboxID int64) (int64, error) {
	stmt := conn.Prep(`SELECT count(*) FROM Msgs
		WHERE State = 1 AND MailboxID = $mailboxID;`)
	stmt.SetInt64("$mailboxID", mailboxID)
	count, err := sqlitex.ResultInt64(stmt)
	if err != nil {
		return 0, fmt.Errorf("mailbox count: %v", err)
	}
	return count, nil
}

func InsertPartSummary(conn *sqlite.Conn, msgID email.MsgID, part *email.Part) error {
	stmt := conn.Prep(`INSERT INTO MsgParts (
			MsgID,
			PartNum, Name, IsBody, IsAttachment, IsCompressed, CompressedSize,
			ContentType, ContentID,
			BlobID,
			ContentTransferEncoding, ContentTransferSize,
			ContentTransferLines
		) VALUES (
			$MsgID,
			$PartNum, $Name, $IsBody, $IsAttachment, $IsCompressed, $CompressedSize,
			$ContentType, $ContentID,
			$BlobID,
			$ContentTransferEncoding, $ContentTransferSize,
			$ContentTransferLines
		);`)
	stmt.SetInt64("$MsgID", int64(msgID))
	stmt.SetInt64("$PartNum", int64(part.PartNum))
	stmt.SetText("$Name", part.Name)
	stmt.SetBool("$IsBody", part.IsBody)
	stmt.SetBool("$IsAttachment", part.IsAttachment)
	stmt.SetBool("$IsCompressed", part.IsCompressed)
	if part.IsCompressed {
		stmt.SetInt64("$CompressedSize", part.CompressedSize)
	}
	stmt.SetText("$ContentType", part.ContentType)
	stmt.SetText("$ContentID", part.ContentID)
	stmt.SetInt64("$BlobID", part.BlobID)
	stmt.SetText("$ContentTransferEncoding", part.ContentTransferEncoding)
	stmt.SetInt64("$ContentTransferSize", part.ContentTransferSize)
	stmt.SetInt64("$ContentTransferLines", part.ContentTransferLines)
	if _, err := stmt.Step(); err != nil {
		return err
	}
	return nil
}

func insertPart(conn *sqlite.Conn, msgID email.MsgID, part *email.Part) (err error) {
	if part.BlobID == 0 {
		stmt := conn.Prep(`INSERT INTO blobs.Blobs (BlobID, Content) VALUES ($BlobID, $Content);`)
		if part.Content == nil {
			stmt.SetNull("$Content")
		} else {
			sz := part.Content.Size()
			if part.IsCompressed {
				sz = part.CompressedSize
			}
			stmt.SetZeroBlob("$Content", sz)
		}
		part.BlobID, err = InsertRandID(stmt, "$BlobID")
		if err != nil {
			return err
		}
		if err := InsertPartSummary(conn, msgID, part); err != nil {
			return err
		}
	} else {
		if part.Content == nil {
			return nil
		}
		stmt := conn.Prep("UPDATE blobs.Blobs SET Content = $Content WHERE BlobID = $BlobID;")
		stmt.SetInt64("$BlobID", part.BlobID)
		sz := part.Content.Size()
		if part.IsCompressed {
			sz = part.CompressedSize
		}
		stmt.SetZeroBlob("$Content", sz)
		if _, err := stmt.Step(); err != nil {
			return err
		}
	}

	if part.Content == nil {
		return nil
	}
	part.Content.Seek(0, 0)
	defer part.Content.Seek(0, 0)

	blob, err := conn.OpenBlob("blobs", "Blobs", "Content", part.BlobID, true)
	if err != nil {
		return err
	}

	h := sha256.New()
	blobAndHash := io.MultiWriter(blob, h)

	if part.IsCompressed {
		gzw := gzip.NewWriter(blobAndHash)
		_, err := io.Copy(gzw, part.Content)
		if err != nil {
			blob.Close()
			return err
		}
		if err := gzw.Close(); err != nil {
			blob.Close()
			return err
		}
	} else {
		if _, err := io.Copy(blobAndHash, part.Content); err != nil {
			blob.Close()
			return err
		}
	}
	if err := blob.Close(); err != nil {
		return err
	}

	stmt := conn.Prep("UPDATE blobs.Blobs SET SHA256 = $SHA256 WHERE BlobID = $BlobID;")
	stmt.SetInt64("$BlobID", part.BlobID)
	stmt.SetText("$SHA256", hex.EncodeToString(h.Sum(make([]byte, 0, sha256.Size))))
	_, err = stmt.Step()
	return err
}

func encodeFlags(buf *bytes.Buffer, flags []string) {
	buf.WriteByte('{')
	for i, flag := range flags {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprintf(buf, "%q: 1", flag)
	}
	buf.WriteByte('}')
}

func (c *Box) setMsgFetched(conn *sqlite.Conn, msgID email.MsgID, provMailboxID int64) (mailboxID int64, err error) {
	stmt := conn.Prep(`UPDATE Msgs SET State = $msgReady
		WHERE MsgID = $msgID AND State = $msgFetching;`)
	stmt.SetInt64("$msgReady", int64(MsgReady))
	stmt.SetInt64("$msgFetching", int64(MsgFetching))
	stmt.SetInt64("$msgID", int64(msgID))
	if _, err := stmt.Step(); err != nil {
		return 0, err
	}

	mailboxID, err = assignMailbox(conn, msgID, provMailboxID)
	if err != nil {
		return 0, err
	}
	if _, err := assignConvo(conn, msgID); err != nil {
		return 0, err
	}

	return mailboxID, nil
}

func assignMailbox(conn *sqlite.Conn, msgID email.MsgID, provMailboxID int64) (mailboxID int64, err error) {
	hdr, err := LoadMsgHdrs(conn, msgID)
	if err != nil {
		return 0, err
	}

	stmt := conn.Prep("SELECT HasUnsubscribe FROM Msgs WHERE MsgID = $msgID;")
	stmt.SetInt64("$msgID", int64(msgID))
	unsubInt, err := sqlitex.ResultInt(stmt)
	if err != nil {
		return 0, err
	}
	hasUnsubscribe := unsubInt > 0

	if provMailboxID != 0 {
		mailboxID = provMailboxID
	} else {
		stmt = conn.Prep(`SELECT MailboxID FROM Mailboxes WHERE Name = $name;`)
		if isSubscription(*hdr) || hasUnsubscribe {
			stmt.SetText("$name", "Subscriptions")
		} else {
			stmt.SetText("$name", "INBOX")
		}
		mailboxID, err = sqlitex.ResultInt64(stmt)
		if err != nil {
			return 0, err
		}
	}
	uid, err := NextMsgUID(conn, mailboxID)
	if err != nil {
		return 0, err
	}

	modSeq, err := NextMsgModSeq(conn, mailboxID)
	if err != nil {
		return 0, err
	}

	stmt = conn.Prep(`UPDATE Msgs SET
		MailboxID = $mailboxID,
		UID = $uid,
		ModSequence = $modSeq
		WHERE MsgID = $msgID;`)
	stmt.SetInt64("$mailboxID", mailboxID)
	stmt.SetInt64("$msgID", int64(msgID))
	stmt.SetInt64("$uid", int64(uid))
	stmt.SetInt64("$modSeq", modSeq)
	if _, err := stmt.Step(); err != nil {
		return 0, err
	}

	return mailboxID, nil
}

func assignConvo(conn *sqlite.Conn, msgID email.MsgID) (convoID ConvoID, err error) {
	defer sqlitex.Save(conn)(&err)

	stmt := conn.Prep(`WITH TheseContacts AS (
			SELECT ContactID From Addresses
			INNER JOIN MsgAddresses ON Addresses.AddressID = MsgAddresses.AddressID
			WHERE MsgID = $msgID
			AND ContactID <> 1
		)
		SELECT ConvoID FROM ConvoContacts
		WHERE ContactID IN TheseContacts
		GROUP BY 1
		HAVING count(*) = (SELECT count(*) FROM TheseContacts);`)
	stmt.SetInt64("$msgID", int64(msgID))
	if exists, err := stmt.Step(); err != nil {
		return 0, err
	} else if exists {
		convoID = ConvoID(stmt.GetInt64("ConvoID"))
		stmt.Reset()
	} else {
		convoID, err = newConvo(conn, msgID)
	}

	stmt = conn.Prep("UPDATE Msgs SET ConvoID = $convoID WHERE MsgID = $msgID;")
	stmt.SetInt64("$msgID", int64(msgID))
	stmt.SetInt64("$convoID", int64(convoID))
	_, err = stmt.Step()
	// TODO: "UPDATE Convos SET Archived = FALSE;"
	return convoID, err
}

func newConvo(conn *sqlite.Conn, msgID email.MsgID) (convoID ConvoID, err error) {
	summary := ConvoSummary{}
	stmt := conn.Prep(`SELECT Name, Address, ContactID FROM Addresses
		INNER JOIN MsgAddresses ON Addresses.AddressID = MsgAddresses.AddressID
		WHERE MsgID = $msgID AND ContactID <> 1;`)
	stmt.SetInt64("$msgID", int64(msgID))
	for {
		if hasNext, err := stmt.Step(); err != nil {
			return 0, err
		} else if !hasNext {
			break
		}
		name := stmt.GetText("Name")
		if name == "" {
			name = strings.ToLower(stmt.GetText("Address"))
		}
		contactID := ContactID(stmt.GetInt64("ContactID"))
		summary.Contacts = append(summary.Contacts, Contact{
			ContactID: contactID,
			Name:      name,
		})
	}
	// TODO ? sort.Slice(summary.Contacts, func(i, j int) bool {})
	summaryBytes, err := json.Marshal(summary)
	if err != nil {
		return 0, err
	}

	stmt = conn.Prep("INSERT INTO Convos (ConvoID, ConvoSummary) VALUES ($convoID, $summary);")
	stmt.SetBytes("$summary", summaryBytes)
	stmt.SetInt64("$convoID", int64(msgID))
	if _, err := stmt.Step(); sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_PRIMARYKEY {
		id, err := InsertRandID(stmt, "$convoID")
		if err != nil {
			return 0, err
		}
		convoID = ConvoID(id)
	} else if err != nil {
		return 0, err
	} else {
		convoID = ConvoID(conn.LastInsertRowID())
	}

	stmt = conn.Prep(`INSERT INTO ConvoContacts (ConvoID, ContactID)
			SELECT DISTINCT $convoID, ContactID FROM Addresses
			INNER JOIN MsgAddresses ON Addresses.AddressID = MsgAddresses.AddressID
			WHERE MsgID = $msgID AND ContactID <> 1;`)
	stmt.SetInt64("$convoID", int64(convoID))
	stmt.SetInt64("$msgID", int64(msgID))
	if _, err := stmt.Step(); err != nil {
		return 0, err
	}

	if err := assignLabel(conn, msgID, convoID); err != nil {
		return 0, err
	}

	return convoID, nil
}

func assignLabel(conn *sqlite.Conn, msgID email.MsgID, convoID ConvoID) (err error) {
	hdr, err := LoadMsgHdrs(conn, msgID)
	if err != nil {
		return err
	}

	stmt := conn.Prep("SELECT HasUnsubscribe FROM Msgs WHERE MsgID = $msgID;")
	stmt.SetInt64("$msgID", int64(msgID))
	unsubInt, err := sqlitex.ResultInt(stmt)
	if err != nil {
		return err
	}
	hasUnsubscribe := unsubInt > 0

	labelPersonalMail, err := findLabel(conn, "Personal Mail")
	if err != nil {
		return err
	}
	labelID := labelPersonalMail

	isSub := isSubscription(*hdr)
	if hasUnsubscribe {
		isSub = true
	}
	if isSub {
		labelID, err = findLabel(conn, "Subscriptions")
		if err != nil {
			return err
		}
	}

	if err := sqlitex.Exec(conn, "INSERT INTO ConvoLabels (LabelID, ConvoID) VALUES (?, ?);", nil, labelID, convoID); err != nil {
		return fmt.Errorf("%v: convoID=%d, labelID=%d", err, convoID, labelID)
	}

	return nil
}

func isSubscription(hdr email.Header) bool {
	switch {
	case len(hdr.Get("List-Id")) > 0 || len(hdr.Get("List-Post")) > 0 || len(hdr.Get("List-Unsubscribe")) > 0:
		return true
	case len(hdr.Get("X-Mandrill-User")) > 0:
		return true // it's mailchimp
	case noreplyRE.Match(hdr.Get("From")):
		return true
	case noreplyDomainRE.Match(hdr.Get("From")):
		return true
	case len(hdr.Get("Feedback-Id")) > 0: // The gmail Feedback Loop (FBL).
		return true
	case len(hdr.Get("EcrmHeader")) > 0: // ECRM is marketing. Used by Verizon.
		return true
	}
	return false
}

func NextMsgUID(conn *sqlite.Conn, mailboxID int64) (uint32, error) {
	stmt := conn.Prep(`SELECT NextUID FROM Mailboxes WHERE MailboxID = $mailboxID;`)
	stmt.SetInt64("$mailboxID", mailboxID)
	nextUID, err := sqlitex.ResultInt64(stmt)
	if err != nil {
		return 0, err
	}

	stmt = conn.Prep(`UPDATE Mailboxes SET NextUID = $new
		WHERE MailboxID = $mailboxID AND NextUID = $new - 1;`)
	stmt.SetInt64("$mailboxID", mailboxID)
	stmt.SetInt64("$new", nextUID+1)
	if _, err := stmt.Step(); err != nil {
		return 0, err
	}

	return uint32(nextUID), nil
}

func NextMsgModSeq(conn *sqlite.Conn, mailboxID int64) (modSeq int64, err error) {
	defer sqlitex.Save(conn)(&err)

	stmt := conn.Prep(`SELECT NextModSequence FROM MailboxSequencing
		WHERE Name IN (
			SELECT Name FROM Mailboxes
			WHERE MailboxID = $mailboxID
		);`)
	stmt.SetInt64("$mailboxID", mailboxID)
	modSeq, err = sqlitex.ResultInt64(stmt)
	if err != nil {
		return 0, err
	}
	stmt = conn.Prep(`UPDATE MailboxSequencing
		SET NextModSequence = NextModSequence + 1
		WHERE Name IN (
			SELECT Name FROM Mailboxes
			WHERE MailboxID = $mailboxID
		);`)
	stmt.SetInt64("$mailboxID", mailboxID)
	if _, err := stmt.Step(); err != nil {
		return 0, err
	}
	return modSeq, nil
}
