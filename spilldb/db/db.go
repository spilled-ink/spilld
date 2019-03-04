package db

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"golang.org/x/crypto/bcrypt"
	"spilled.ink/third_party/imf"
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

func Open(dbfile string) (*sqlitex.Pool, error) {
	conn, err := sqlite.OpenConn(dbfile, 0)
	if err != nil {
		return nil, fmt.Errorf("db.Open: main init open: %v", err)
	}
	if err := Init(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db.Open: main init: %v", err)
	}
	if err := conn.Close(); err != nil {
		return nil, fmt.Errorf("db.Open: main init close: %v", err)
	}
	db, err := sqlitex.Open(dbfile, 0, 24)
	if err != nil {
		return nil, fmt.Errorf("db.Open: main pool: %v", err)
	}
	return db, nil
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
	EmailAddr     string // user@domain
	Password      string
	Admin         bool
}

func (details *UserDetails) Validate() error {
	//if fullname == "" {
	//	return &UserError{UserMsg: "missing full name"}
	//}
	if len(details.FullName) > 150 {
		return &UserError{UserMsg: "full name too long"}
	}
	if len(details.Password) < 8 {
		return &UserError{UserMsg: "password less than 8 characters"}
	}
	if _, err := imf.ParseAddress(details.EmailAddr); err != nil {
		return &UserError{UserMsg: err.Error()}
	}
	return nil
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
			$passHash, $secretBoxKey, $admin, FALSE
		);`)
	stmt.SetText("$fullName", details.FullName)
	stmt.SetText("$phoneNumber", details.PhoneNumber)
	stmt.SetBool("$phoneVerified", details.PhoneVerified)
	stmt.SetBytes("$passHash", passHash)
	stmt.SetText("$secretBoxKey", hex.EncodeToString(secretBoxKey))
	stmt.SetBool("$admin", details.Admin)
	userID, err = sqlitex.InsertRandID(stmt, "$userID", 1, 1<<23)
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_UNIQUE {
			return 0, ErrUserUnavailable
		}
		return 0, err
	}

	if err := AddUserAddress(conn, userID, details.EmailAddr, true); err != nil {
		return 0, err
	}

	return userID, nil
}

func AddUserAddress(conn *sqlite.Conn, userID int64, addr string, primaryAddr bool) error {
	if strings.LastIndexByte(addr, '@') == -1 {
		return &UserError{UserMsg: "Invalid email address, missing @domain."}
	}

	stmt := conn.Prep(`INSERT INTO UserAddresses (Address, UserID, PrimaryAddr) VALUES ($addr, $userID, $primaryAddr);`)
	stmt.SetText("$addr", strings.ToLower(addr))
	stmt.SetInt64("$userID", userID)
	stmt.SetBool("$primaryAddr", primaryAddr)
	if _, err := stmt.Step(); err != nil {
		if sqlite.ErrCode(err) == sqlite.SQLITE_CONSTRAINT_PRIMARYKEY {
			return &UserError{UserMsg: fmt.Sprintf("Address %q is already assigned.", addr)}
		}
		return err
	}

	if primaryAddr {
		stmt = conn.Prep(`UPDATE UserAddresses SET PrimaryAddr = FALSE WHERE UserID = $userID AND Address <> $addr;`)
		stmt.SetText("$addr", addr)
		stmt.SetInt64("$userID", userID)
		if _, err := stmt.Step(); err != nil {
			return err
		}
	}

	return nil
}

func SetUserPrimaryAddr(conn *sqlite.Conn, userID int64, addr string) error {
	stmt := conn.Prep(`UPDATE UserAddresses SET PrimaryAddr = (CASE WHEN Address = $addr THEN TRUE ELSE FALSE END) WHERE UserID = $userID;`)
	stmt.SetText("$addr", addr)
	stmt.SetInt64("$userID", userID)
	if _, err := stmt.Step(); err != nil {
		return err
	}
	if conn.Changes() == 0 {
		return fmt.Errorf("db.SetUserPrimaryAddr: unknown address")
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

type Log struct {
	Where    string
	What     string
	When     time.Time
	Duration time.Duration
	Err      error
	Data     map[string]interface{}
}

func (l Log) String() string {
	buf := new(strings.Builder)
	fmt.Fprintf(buf, `{"where": %q, "what": %q, `, l.Where, l.What)

	buf.WriteString(`"when": "`)
	buf.Write(l.When.AppendFormat(make([]byte, 0, 64), time.RFC3339Nano))
	buf.WriteString(`"`)

	fmt.Fprintf(buf, `, "duration": "%s"`, l.Duration)

	if l.Err != nil {
		fmt.Fprintf(buf, `, "err": %q`, l.Err.Error())
	}
	if len(l.Data) > 0 {
		b, err := json.Marshal(l.Data)
		if err != nil {
			fmt.Fprintf(buf, `, "data_marshal_err": %q`, err.Error())
		} else {
			fmt.Fprintf(buf, `, "data": %s`, b)
		}
	}
	buf.WriteByte('}')
	return buf.String()
}
