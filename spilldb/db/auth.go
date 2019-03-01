package db

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"crawshaw.io/sqlite/sqlitex"

	"golang.org/x/crypto/bcrypt"
	"spilled.ink/util/throttle"
)

type Authenticator struct {
	DB       *sqlitex.Pool
	Throttle throttle.Throttle
	Logf     func(format string, v ...interface{})
	Where    string
}

var errAuthFailed = errors.New("authenticator: internal error")
var errPassDeleted = errors.New("authenticator: password deleted")
var ErrBadCredentials = errors.New("authenticator: bad credentials")

func (a *Authenticator) AuthDevice(ctx context.Context, remoteAddr, username string, password []byte) (userID int64, err error) {
	conn := a.DB.Get(ctx)
	if conn == nil {
		return 0, context.Canceled
	}
	defer a.DB.Put(conn)

	start := time.Now()
	log := &Log{
		Where: a.Where,
		What:  "auth",
		When:  start,
		Data: map[string]interface{}{
			"remote_addr": remoteAddr,
			"username":    username,
		},
	}
	defer func() {
		log.Duration = time.Since(start)
		a.Logf("%s", log.String())
	}()

	password = bytes.ToUpper(password)
	password = bytes.Replace(password, []byte(" "), []byte(""), -1)

	a.Throttle.Throttle(remoteAddr)
	a.Throttle.Throttle(username)
	defer func() {
		if err != nil {
			a.Throttle.Add(remoteAddr)
			a.Throttle.Add(username)
		}
	}()

	var devices int
	var deviceID int64
	stmt := conn.Prep(`SELECT DeviceID, UserID, AppPassHash, Deleted FROM Devices
		WHERE UserID IN (SELECT UserID FROM UserAddresses WHERE Address = $username);`)
	stmt.SetText("$username", username)
	for {
		if hasNext, err := stmt.Step(); err != nil {
			log.Err = err
			return 0, errAuthFailed
		} else if !hasNext {
			break
		}
		devices++

		passHash := []byte(stmt.GetText("AppPassHash"))
		if err := bcrypt.CompareHashAndPassword(passHash, password); err == nil {
			deleted := stmt.GetInt64("Deleted") != 0
			deviceID = stmt.GetInt64("DeviceID")
			userID = stmt.GetInt64("UserID")
			stmt.Reset()

			if deleted {
				log.Err = errPassDeleted
				return 0, ErrBadCredentials
			}
			break
		}
	}
	log.Data["device_id"] = deviceID
	if devices == 0 {
		log.Err = errors.New("unknown username")
		return 0, ErrBadCredentials
	} else if userID == 0 {
		log.Err = errors.New("bad password")
		return 0, ErrBadCredentials
	}
	log.Data["user_id"] = userID

	stmt = conn.Prep(`UPDATE Devices
		SET LastAccessTime = $time, LastAccessAddr = $addr
		WHERE DeviceID = $deviceID;`)
	stmt.SetInt64("$deviceID", deviceID)
	stmt.SetInt64("$time", time.Now().Unix())
	stmt.SetText("$addr", remoteAddr)
	if _, err := stmt.Step(); err != nil {
		log.Err = fmt.Errorf("device update failed: %v", err)
		return 0, errAuthFailed
	}

	return userID, nil
}
