// Package smtpdb glues smtpserver into the database.
package smtpdb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/smtp/smtpserver"
	"spilled.ink/spilldb/db"
)

type MsgMaker struct {
	ctx       context.Context
	dbpool    *sqlitex.Pool
	filer     *iox.Filer
	msgDoneFn func(stagingID int64)
	auth      *db.Authenticator
}

func New(ctx context.Context, dbpool *sqlitex.Pool, filer *iox.Filer, doneFn func(stagingID int64)) *MsgMaker {
	logf := log.Printf // TODO
	p := &MsgMaker{
		ctx:       ctx,
		dbpool:    dbpool,
		filer:     filer,
		msgDoneFn: doneFn,
		auth: &db.Authenticator{
			DB:    dbpool,
			Logf:  logf,
			Where: "smtp",
		},
	}
	return p
}

func (p *MsgMaker) Auth(identity, user, password []byte, remoteAddr string) uint64 {
	userID, err := p.auth.AuthDevice(p.ctx, remoteAddr, string(user), password)
	if err != nil {
		return 0 // logging done by AuthDevice method
	}
	return uint64(userID)
}

func (p *MsgMaker) NewMessage(remoteAddr net.Addr, from []byte, authToken uint64) (smtpserver.Msg, error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return nil, context.Canceled
	}
	defer p.dbpool.Put(conn)

	if authToken != 0 {
		// Confirm the sender is allowed to use this source address.
		stmt := conn.Prep(`SELECT UserID FROM UserAddresses WHERE Address = $address;`)
		stmt.SetBytes("$address", from)
		if hasNext, err := stmt.Step(); err != nil {
			return nil, err
		} else if !hasNext {
			// TODO: log invalid address
			return nil, fmt.Errorf("bad sender address")
		}
		userID := stmt.GetInt64("UserID")
		stmt.Reset()
		if userID != int64(authToken) {
			// TODO: log that user does not own address
			return nil, fmt.Errorf("bad sender address")
		}
	}

	stmt := conn.Prep("INSERT INTO Msgs (UserID, Sender, DateReceived) VALUES ($userID, $sender, $time);")
	stmt.SetInt64("$userID", int64(authToken))
	stmt.SetBytes("$sender", from)
	stmt.SetInt64("$time", time.Now().Unix())
	if _, err := stmt.Step(); err != nil {
		return nil, err
	}
	m := &smtpMsg{
		ctx:       p.ctx,
		dbpool:    p.dbpool,
		filer:     p.filer,
		msgDoneFn: p.msgDoneFn,
		stagingID: conn.LastInsertRowID(),
		auth:      authToken != 0,
	}
	return m, nil
}

type smtpMsg struct {
	ctx       context.Context
	dbpool    *sqlitex.Pool
	filer     *iox.Filer
	msgDoneFn func(stagingID int64)
	stagingID int64
	f         *iox.BufferFile
	auth      bool
	err       error
}

func (m *smtpMsg) AddRecipient(addr []byte) (bool, error) {
	conn := m.dbpool.Get(m.ctx)
	if conn == nil {
		return false, context.Canceled
	}
	defer m.dbpool.Put(conn)

	var domain []byte
	if i := bytes.IndexByte(addr, '@'); i > 0 && i+1 < len(addr) {
		domain = addr[i+1:]
	}
	asciiLower(domain)

	localDomain := false
	log.Printf("AddRecipient domain=%q", string(domain))
	switch string(domain) {
	case "spilled.ink":
		localDomain = true
	case "gmail.com", "yahoo.com", "aol.com", "msn.com", "facebook.com", "googlegroups.com":
		localDomain = false
	default:
		stmt := conn.Prep(`SELECT count(*) From Domains WHERE DomainName = $name;`)
		stmt.SetBytes("$name", domain)
		count, err := sqlitex.ResultInt(stmt)
		if err != nil {
			return false, err
		}
		localDomain = count != 0
	}
	if localDomain {
		asciiLower(addr)
	}

	// Unauthenticated message sends or messages sent to a local domain
	// must go to valid local recipients.
	// Otherwise you can send anywhere.
	if !m.auth || localDomain {
		stmt := conn.Prep(`SELECT UserID From UserAddresses WHERE Address = $address;`)
		stmt.SetBytes("$address", addr)
		if hasRow, err := stmt.Step(); err != nil {
			log.Printf("accountaddresses err: %v", err)
			return false, err
		} else if !hasRow {
			log.Printf("invalid recipient: %q", addr)
			return false, nil
		}
		userID := stmt.GetInt64("UserID")
		stmt.Reset()

		if userID == 0 {
			log.Printf("invalid recipient user: %q", addr)
			return false, nil
		}
	}

	stmt := conn.Prep("INSERT INTO MsgRecipients (StagingID, Recipient, FullAddress, DeliveryState) VALUES ($stagingID, $address, '', $deliveryState);")
	stmt.SetInt64("$stagingID", m.stagingID)
	stmt.SetInt64("$deliveryState", int64(db.DeliveryReceiving))
	stmt.SetBytes("$address", addr)
	_, err := stmt.Step()
	if sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_PRIMARYKEY {
		log.Printf("stagingID %d: could not add recipient: %s", m.stagingID, addr)
		return false, nil
	} else if err != nil {
		m.err = err
		return false, err
	}
	return true, nil
}

func (m *smtpMsg) Write(line []byte) error {
	if m.err != nil {
		return m.err
	}
	if m.f == nil {
		m.f = m.filer.BufferFile(0)
	}
	_, err := m.f.Write(line)
	if err != nil && m.err == nil {
		m.err = err
	}
	return err
}

func (m *smtpMsg) Cancel() {
	if m.err == nil {
		m.err = context.Canceled
	}
	m.f.Close()
	m.f = nil
	m.removeMsg()
}

func (m *smtpMsg) removeMsg() {
	if m.stagingID == 0 {
		return
	}

	conn := m.dbpool.Get(m.ctx)
	if conn == nil {
		return
	}
	defer m.dbpool.Put(conn)

	log.Printf("removing stagingID=%d", m.stagingID)
	stmt := conn.Prep("DELETE FROM MsgRecipients WHERE StagingID = $stagingID;")
	stmt.SetInt64("$stagingID", m.stagingID)
	if _, err := stmt.Step(); err != nil {
		log.Printf("failed to clean up msg recipients: %v", err)
	}
	stmt = conn.Prep("DELETE FROM Msgs WHERE StagingID = $stagingID;")
	stmt.SetInt64("$stagingID", m.stagingID)
	if _, err := stmt.Step(); err != nil {
		log.Printf("failed to clean up msg: %v", err)
	}
}

func (m *smtpMsg) Close() (err error) {
	if m.err != nil {
		return m.err
	}
	if m.f == nil {
		m.err = fmt.Errorf("s%d: no message body", m.stagingID)
		return m.err
	}
	defer func() {
		m.f.Close()
		m.f = nil
		if m.err != nil {
			m.removeMsg()
		}
		if err == nil {
			err = m.err
		}
	}()

	conn := m.dbpool.Get(m.ctx)
	if conn == nil {
		return context.Canceled
	}
	defer m.dbpool.Put(conn)

	if m.err = saveMsg(conn, m.stagingID, m.f); m.err != nil {
		return m.err
	}

	if _, m.err = m.f.Seek(0, 0); m.err != nil {
		return m.err
	}

	if !m.auth {
		// All recipients are local, because we are never an open relay.
		// Incoming message for us locally.
		stmt := conn.Prep(`UPDATE MsgRecipients
			SET DeliveryState = $deliveryToProcess
			WHERE StagingID = $stagingID;`)
		stmt.SetInt64("$stagingID", m.stagingID)
		stmt.SetInt64("$deliveryToProcess", int64(db.DeliveryToProcess))
		if _, m.err = stmt.Step(); m.err != nil {
			return m.err
		}
	} else {
		// Received a client mail submission.
		// Some recipients may be local, some remote.
		stmt := conn.Prep(`UPDATE MsgRecipients
			SET DeliveryState = $deliveryToProcess
			WHERE StagingID = $stagingID
			AND Recipient IN (
				SELECT Recipient FROM MsgRecipients
				INNER JOIN UserAddresses ON Address = Recipient
				WHERE StagingID = $stagingID
			);`)
		stmt.SetInt64("$stagingID", m.stagingID)
		stmt.SetInt64("$deliveryToProcess", int64(db.DeliveryToProcess))
		if _, m.err = stmt.Step(); m.err != nil {
			return m.err
		}

		// Mark the remaining recipients for external delivery.
		stmt = conn.Prep(`UPDATE MsgRecipients
			SET DeliveryState = $deliverySending
			WHERE StagingID = $stagingID
			AND DeliveryState = $deliveryReceiving;`)
		stmt.SetInt64("$stagingID", m.stagingID)
		stmt.SetInt64("$deliverySending", int64(db.DeliverySending))
		stmt.SetInt64("$deliveryReceiving", int64(db.DeliveryReceiving))
		if _, m.err = stmt.Step(); m.err != nil {
			return m.err
		}
	}

	if m.msgDoneFn != nil {
		m.msgDoneFn(m.stagingID)
	}
	return nil
}

func saveMsg(conn *sqlite.Conn, stagingID int64, f *iox.BufferFile) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}

	stmt := conn.Prep("INSERT INTO MsgRaw (StagingID, Content) VALUES ($stagingID, $content);")
	stmt.SetInt64("$stagingID", stagingID)
	stmt.SetZeroBlob("$content", f.Size())
	if _, err := stmt.Step(); err != nil {
		return err
	}
	stmt.Reset()
	b, err := conn.OpenBlob("", "MsgRaw", "Content", stagingID, true)
	if err != nil {
		return err
	}
	defer b.Close()
	if _, err := io.Copy(b, f); err != nil {
		return err
	}
	return nil
}

func asciiLower(data []byte) {
	for i, b := range data {
		if b >= 'A' && b <= 'Z' {
			data[i] = b + ('a' - 'A')
		}
	}
}
