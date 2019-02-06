// Package localsender moves messages from the main database to user databases.
package localsender

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/email"
	"spilled.ink/email/msgcleaver"
	"spilled.ink/spilldb/boxmgmt"
	"spilled.ink/spilldb/db"
	"spilled.ink/spilldb/spillbox"
)

type LocalSender struct {
	ctx      context.Context
	cancelFn func()
	done     chan struct{}

	dbpool  *sqlitex.Pool
	filer   *iox.Filer
	boxmgmt *boxmgmt.BoxMgmt

	newmsg chan struct{}

	maxReadyDateMu sync.Mutex
	maxReadyDate   int64
}

func New(dbpool *sqlitex.Pool, filer *iox.Filer, boxmgmt *boxmgmt.BoxMgmt) *LocalSender {
	ctx, cancelFn := context.WithCancel(context.Background())
	return &LocalSender{
		ctx:      ctx,
		cancelFn: cancelFn,
		done:     make(chan struct{}),

		dbpool:  dbpool,
		filer:   filer,
		boxmgmt: boxmgmt,

		newmsg: make(chan struct{}, 1),
	}
}

func (p *LocalSender) Process(stagingID int64) {
	// It is OK to drop messages here, they will be
	// picked up on the periodic DB scan.
	select {
	case p.newmsg <- struct{}{}:
	default:
	}
}

func (p *LocalSender) Shutdown(ctx context.Context) {
	p.cancelFn()
	<-p.done
}

func (p *LocalSender) Run() error {
	defer func() { close(p.done) }()

	if err := p.loadMaxReadyDate(); err != nil {
		if err == context.Canceled {
			return nil
		}
		return err
	}

	ticker := time.NewTicker(2 * time.Second)
	for {
		select {
		case <-p.ctx.Done():
			return nil
		case <-p.newmsg:
		case <-ticker.C:
		}

		toSend, more, err := p.collectToSend()
		if err != nil {
			if err == context.Canceled {
				return nil
			}
			return err
		}

		if more {
			// There are probably more messages ready to process.
			// Prime the pump for the next cycle.
			select {
			case p.newmsg <- struct{}{}:
			default:
			}
		}

		var wg sync.WaitGroup
		for _, userID := range toSend {
			wg.Add(1)
			go func(userID int64) {
				defer wg.Done()
				err := p.sendForUser(userID)
				if err != nil {
					// TODO plumb logging
					log.Printf("localsend %v: %v", userID, err)
				}
			}(userID)
		}
		wg.Wait()
	}
}

func (p *LocalSender) loadMaxReadyDate() error {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return context.Canceled
	}
	defer p.dbpool.Put(conn)

	v, err := sqlitex.ResultInt64(conn.Prep("SELECT ifnull(max(ReadyDate), 0) FROM Msgs;"))
	if err != nil {
		return err
	}

	p.maxReadyDateMu.Lock()
	p.maxReadyDate = v
	p.maxReadyDateMu.Unlock()

	return nil
}

func (p *LocalSender) collectToSend() (toSend []int64, more bool, err error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return nil, false, context.Canceled
	}
	defer p.dbpool.Put(conn)

	const limit = 8

	stmt := conn.Prep(`SELECT DISTINCT UserID
		FROM MsgRecipients
		INNER JOIN UserAddresses ON UserAddresses.Address = MsgRecipients.Recipient
		WHERE DeliveryState = $deliveryState
		ORDER BY UserID LIMIT $limit;`)
	stmt.SetInt64("$deliveryState", int64(db.DeliveryReceived))
	stmt.SetInt64("$limit", limit)

	for {
		if hasNext, err := stmt.Step(); err != nil {
			return nil, false, err
		} else if !hasNext {
			break
		}
		userID := stmt.GetInt64("UserID")
		toSend = append(toSend, userID)
	}

	more = len(toSend) == limit
	return toSend, more, nil
}

func (p *LocalSender) collectMsgsToSend(userID int64) ([]int64, error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return nil, context.Canceled
	}
	defer p.dbpool.Put(conn)

	return db.CollectMsgsToSend(conn, userID, 10, 0)
}

func (p *LocalSender) setMsgsSent(userID int64, stagingIDs []int64) (err error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return context.Canceled
	}
	defer p.dbpool.Put(conn)
	defer sqlitex.Save(conn)(&err)

	stmt := conn.Prep(`UPDATE MsgRecipients
		SET DeliveryState = $deliveryDone
		WHERE StagingID = $stagingID
		AND DeliveryState = $deliveryReceived
		AND Recipient IN (SELECT Address FROM UserAddresses WHERE UserID = $userID);`)
	stmt.SetInt64("$deliveryReceived", int64(db.DeliveryReceived))
	stmt.SetInt64("$deliveryDone", int64(db.DeliveryDone))
	stmt.SetInt64("$userID", userID)

	for _, stagingID := range stagingIDs {
		stmt.Reset()
		stmt.SetInt64("$stagingID", stagingID)
		if _, err := stmt.Step(); err != nil {
			return err
		}
	}

	return nil
}

func (p *LocalSender) loadMsg(stagingID int64) (*iox.BufferFile, time.Time, error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return nil, time.Time{}, context.Canceled
	}
	defer p.dbpool.Put(conn)

	stmt := conn.Prep("SELECT DateReceived FROM Msgs WHERE StagingID = $stagingID;")
	stmt.SetInt64("$stagingID", stagingID)
	dateInt, err := sqlitex.ResultInt64(stmt)
	if err != nil {
		return nil, time.Time{}, err
	}
	date := time.Unix(dateInt, 0)
	buf, err := db.LoadMsg(conn, p.filer, stagingID, false)
	if err != nil {
		return nil, time.Time{}, err
	}
	return buf, date, err
}

func (p *LocalSender) sendForUser(userID int64) (err error) {
	log.Printf("localsend: sending messages for user %v", userID)

	user, err := p.boxmgmt.Open(p.ctx, userID)
	if err != nil {
		return err
	}

	stagingIDs, err := p.collectMsgsToSend(userID)
	if err != nil {
		return err
	}

	for _, stagingID := range stagingIDs {
		if err := p.sendMsg(userID, user, stagingID); err != nil {
			// TODO plumb logging
			log.Printf("localsend(user %d): %v", userID, err)
			// continue, don't let a bad message block others
		}
	}
	return nil
}

func (p *LocalSender) sendMsg(userID int64, user *boxmgmt.User, stagingID int64) (err error) {
	src, date, err := p.loadMsg(stagingID)
	if err != nil {
		src.Close()
		return fmt.Errorf("staging ID %d: %v", stagingID, err)
	}
	msg, err := msgcleaver.Cleave(p.filer, src)
	src.Close()
	if err != nil {
		return fmt.Errorf("staging ID %d: %v", stagingID, err)
	}
	log.Printf("localsender setting date=%v", date)
	msg.Date = date
	err = insertMsg(p.ctx, user.Box, msg, stagingID)
	msg.Close()
	if err != nil {
		return fmt.Errorf("staging ID %d: %v", stagingID, err)
	}

	stagingIDsDone := []int64{stagingID}
	if err := p.setMsgsSent(userID, stagingIDsDone); err != nil {
		return err
	}
	return nil
}

func insertMsg(ctx context.Context, c *spillbox.Box, msg *email.Msg, stagingID int64) (err error) {
	msg.Flags = recentFlag
	done, err := c.InsertMsg(ctx, msg, stagingID)
	if err != nil {
		return err
	}
	if !done {
		return errors.New("localsender: missing message content")
	}
	return nil
}

var recentFlag = []string{`\Recent`}
