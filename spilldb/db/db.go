package db

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"golang.org/x/crypto/bcrypt"
	"spilled.ink/spilldb/spillbox"
)

var ErrUserUnavailable = &UserError{UserMsg: "Username unavailable."}

type DeliveryState int

const (
	DeliveryUnknown   = 0
	DeliveryReceiving = 7 // incoming email, being received
	DeliveryToProcess = 6 // incoming email, needs to be processed
	DeliveryReceived  = 1 // incoming email, ready to deliver
	DeliveryStaging   = 2 // message created, but sendmsg not invoked yet
	DeliverySending   = 3 // sendmsg invoked, deliverer will pick it up
	DeliveryDone      = 4 // no more work to do, message sent
	DeliveryFailed    = 5 // no more work to do, (maybe partially) failed
)

func (d DeliveryState) String() string {
	switch d {
	case DeliveryUnknown:
		return "DeliveryUnknown"
	case DeliveryReceiving:
		return "DeliveryReceiving"
	case DeliveryToProcess:
		return "DeliveryToProcess"
	case DeliveryReceived:
		return "DeliveryReceived"
	case DeliveryStaging:
		return "DeliveryStaging"
	case DeliverySending:
		return "DeliverySending"
	case DeliveryDone:
		return "DeliveryDone"
	case DeliveryFailed:
		return "DeliveryFailed"
	default:
		return fmt.Sprintf("DeliveryState(%d)", int(d))
	}
}

func Init(conn *sqlite.Conn) (err error) {
	if err := sqlitex.ExecTransient(conn, "PRAGMA journal_mode=WAL;", nil); err != nil {
		return err
	}
	if err := sqlitex.ExecTransient(conn, "PRAGMA cache_size = -50000;", nil); err != nil {
		return err
	}
	if err := sqlitex.ExecScript(conn, createSQL); err != nil {
		return err
	}
	return nil
}

func CollectMsgsToSend(conn *sqlite.Conn, userID, limit, minReadyDate int64) (stagingIDs []int64, err error) {
	stmt := conn.Prep(`SELECT Msgs.StagingID, ReadyDate FROM Msgs
		INNER JOIN MsgRecipients ON Msgs.StagingID = MsgRecipients.StagingID
		INNER JOIN UserAddresses ON MsgRecipients.Recipient = UserAddresses.Address
		WHERE UserAddresses.UserID = $userID
			AND DeliveryState = $deliveryState
			AND ReadyDate > $minReadyDate
		ORDER BY Msgs.StagingID
		LIMIT $limit;`)
	stmt.SetInt64("$userID", userID)
	stmt.SetInt64("$deliveryState", int64(DeliveryReceived))
	stmt.SetInt64("$minReadyDate", minReadyDate)
	stmt.SetInt64("$limit", int64(limit))

	for {
		if hasRow, err := stmt.Step(); err != nil {
			return nil, err
		} else if !hasRow {
			break
		}
		stagingIDs = append(stagingIDs, stmt.GetInt64("StagingID"))
	}

	return stagingIDs, nil
}

func LoadMsg(conn *sqlite.Conn, filer *iox.Filer, stagingID int64, raw bool) (*iox.BufferFile, error) {
	tableName := "MsgFull"
	if raw {
		tableName = "MsgRaw"
	}
	msg := filer.BufferFile(0)
	blob, err := conn.OpenBlob("", tableName, "Content", stagingID, false)
	if err != nil {
		msg.Close()
		return nil, err
	}
	_, err = io.Copy(msg, blob)
	blob.Close()
	if err != nil {
		msg.Close()
		return nil, err
	}
	if _, err := msg.Seek(0, 0); err != nil {
		msg.Close()
		return nil, err
	}
	return msg, nil
}

func AddDevice(conn *sqlite.Conn, userID int64, deviceName, appPassword string) (deviceID int64, err error) {
	appPassHash, err := bcrypt.GenerateFromPassword([]byte(appPassword), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}

	stmt := conn.Prep(`INSERT INTO Devices (UserID, DeviceName, AppPassHash, Created)
		VALUES ($userID, $deviceName, $appPassHash, $created);`)
	stmt.SetInt64("$userID", userID)
	stmt.SetText("$deviceName", deviceName)
	stmt.SetBytes("$appPassHash", appPassHash)
	stmt.SetInt64("$created", time.Now().Unix())
	if _, err := stmt.Step(); err != nil {
		return 0, err
	}
	return conn.LastInsertRowID(), nil
}

type UserDetails struct {
	FullName      string
	PhoneNumber   string
	PhoneVerified bool
	Username      string
	Password      string
}

func AddUser(conn *sqlite.Conn, details UserDetails) (userID int64, err error) {
	var passHash []byte
	passHash, err = bcrypt.GenerateFromPassword([]byte(details.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}

	secretBoxKey := make([]byte, 32)
	if _, err := rand.Read(secretBoxKey); err != nil {
		return 0, err
	}

	stmt := conn.Prep(`INSERT INTO Users (
			UserID, FullName, PhoneNumber, PhoneVerified,
			PassHash, SecretBoxKey, Admin, Locked
		) VALUES (
			$userID, $fullName, $phoneNumber, $phoneVerified,
			$passHash, $secretBoxKey, FALSE, FALSE
		);`)
	stmt.SetText("$fullName", details.FullName)
	stmt.SetText("$phoneNumber", details.PhoneNumber)
	stmt.SetBool("$phoneVerified", details.PhoneVerified)
	stmt.SetBytes("$passHash", passHash)
	stmt.SetText("$secretBoxKey", hex.EncodeToString(secretBoxKey))
	userID, err = spillbox.InsertRandID(stmt, "$userID")
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_UNIQUE {
			return 0, ErrUserUnavailable
		}
		return 0, err
	}

	if err := AddUserAddress(conn, userID, details.Username, true); err != nil {
		return 0, err
	}

	return userID, nil
}

func AddUserAddress(conn *sqlite.Conn, userID int64, addr string, primaryAddr bool) error {
	stmt := conn.Prep(`INSERT INTO UserAddresses (Address, UserID, PrimaryAddr) VALUES ($addr, $userID, $primaryAddr);`)
	stmt.SetText("$addr", addr)
	stmt.SetInt64("$userID", userID)
	stmt.SetBool("$primaryAddr", primaryAddr)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	stmt = conn.Prep(`UPDATE UserAddresses SET PrimaryAddr = FALSE WHERE UserID = $userID AND Address <> $addr`)
	stmt.SetText("$addr", addr)
	stmt.SetInt64("$userID", userID)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	return nil
}

// UserError is a user-input error that has a friendly message
// that should be displayed to the user in typical circumstances
// (say, during form validation).
type UserError struct {
	UserMsg string
	Focus   string // UI containing the error (for example, an <input> ID)
	Err     error
}

func (e *UserError) Error() string {
	if e.Err == nil {
		return e.UserMsg
	}
	return fmt.Sprintf("UserError: %s: %v", e.UserMsg, e.Err)
}
