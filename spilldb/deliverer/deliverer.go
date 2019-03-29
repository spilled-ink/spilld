// Package deliverer implements an outbound SMTP message mailer.
package deliverer

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/email/dkim"
	"spilled.ink/email/msgcleaver"
	"spilled.ink/smtp/smtpclient"
	"spilled.ink/spilldb/db"
)

type Deliverer struct {
	ctx      context.Context
	cancelFn func()
	done     chan struct{}

	dbpool *sqlitex.Pool
	filer  *iox.Filer
	client *smtpclient.Client

	newmsg chan struct{}
}

// NewDeliverer creates a Deliverer that periodically scans the DB and delivers emails.
func NewDeliverer(dbpool *sqlitex.Pool, filer *iox.Filer) *Deliverer {
	// TODO: principled source for constants
	const localHostname = "mx.spilledinkmail.com"
	const localAddr = "172.31.24.137"

	ctx, cancelFn := context.WithCancel(context.Background())
	d := &Deliverer{
		ctx:      ctx,
		cancelFn: cancelFn,
		done:     make(chan struct{}),

		dbpool: dbpool,
		filer:  filer,
		client: smtpclient.NewClient(localHostname, 100),
		newmsg: make(chan struct{}, 1),
	}
	if ip := net.ParseIP(localAddr); isLocalAddr(ip) {
		d.client.LocalAddr = &net.TCPAddr{IP: ip}
	}
	return d
}

func isLocalAddr(ip net.IP) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return false
		}
		for _, addr := range addrs {
			var local net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				local = v.IP
			case *net.IPAddr:
				local = v.IP
			default:
				continue
			}
			if local.Equal(ip) {
				return true
			}
		}
	}
	return false
}

func (d *Deliverer) Deliver(stagingID int64) {
	// It is OK to drop messages here, they will be
	// picked up on the DB scan.
	select {
	case d.newmsg <- struct{}{}:
	default:
	}
}

func (d *Deliverer) Shutdown() {
	d.cancelFn()
	<-d.done
}

func (d *Deliverer) recordDelivery(stagingID int64, res []smtpclient.Delivery) error {
	// Do not use the context here.
	// An SMTP send has been successful.
	// Do absolutely everything we can to get this fact recorded.
	conn := d.dbpool.Get(nil)
	defer d.dbpool.Put(conn)

	date := time.Now().Unix()

	stmt := conn.Prep("INSERT INTO Deliveries (StagingID, Recipient, Code, Date, Details) VALUES ($stagingID, $recipient, $code, $date, $details);")
	stmt.SetInt64("$stagingID", stagingID)
	stmt.SetInt64("$date", date)
	for _, d := range res {
		stmt.Reset()
		stmt.SetInt64("$code", int64(d.Code))
		stmt.SetText("$recipient", d.Recipient)
		details := d.Details
		if d.Error != nil {
			if details != "" {
				details += ", "
			}
			details += "error: " + d.Error.Error()
		}
		stmt.SetText("$details", details)
		if _, err := stmt.Step(); err != nil {
			return err
		}
	}

	stmt = conn.Prep("UPDATE MsgRecipients SET DeliveryState = $deliveryDone WHERE StagingID = $stagingID AND Recipient = $recipient;")
	stmt.SetInt64("$stagingID", stagingID)
	stmt.SetInt64("$deliveryDone", int64(db.DeliveryDone))
	for _, d := range res {
		if d.Success() {
			stmt.Reset()
			stmt.SetText("$recipient", d.Recipient)
			if _, err := stmt.Step(); err != nil {
				return err
			}
		}
	}

	return nil
}

func (d *Deliverer) deliver(stagingID int64, from string, recipients []string, contents *iox.BufferFile) error {
	// TODO: remove error return value from Send
	res, _ := d.client.Send(d.ctx, from, recipients, contents, contents.Size())

	if err := d.recordDelivery(stagingID, res); err != nil {
		return err
	}

	conn := d.dbpool.Get(d.ctx)
	defer d.dbpool.Put(conn)

	// Determine permenant delivery failures by looking at the delivery logs.
	stmt := conn.Prep("SELECT Code, Date FROM Deliveries WHERE StagingID = $stagingID AND Recipient = $recipient ORDER BY Date;")
	for _, d := range res {
		if d.Success() {
			continue
		}
		stmt.SetInt64("$stagingID", stagingID)
		stmt.SetText("$recipient", d.Recipient)
		var delivery smtpclient.Delivery
		var pastDeliveries []smtpclient.Delivery
		for {
			if hasNext, err := stmt.Step(); err != nil {
				return err
			} else if !hasNext {
				break
			}
			pastDeliveries = append(pastDeliveries, smtpclient.Delivery{
				Recipient: d.Recipient,
				Code:      int(stmt.GetInt64("Code")),
				Date:      time.Unix(stmt.GetInt64("Date"), 0),
			})
		}
		const retryWindow = 36 * time.Hour
		permFailure := delivery.PermFailure()
		if len(pastDeliveries) > 0 && delivery.Date.Sub(pastDeliveries[0].Date) > retryWindow {
			permFailure = true
		}
		if !permFailure {
			continue
		}

		// TODO: handle permFailure
		log.Printf("TODO: handle perm failure of %v", stagingID)
	}

	// Determine if the message has been completely sent, mark it as such.
	for _, d := range res {
		if !d.Success() {
			continue
		}
	}

	return nil
}

type deliveryData struct {
	stagingID  int64
	from       string
	recipients []string
	contents   *iox.BufferFile
}

func (d *Deliverer) collectToDeliver() (deliveries []deliveryData, more bool, err error) {
	conn := d.dbpool.Get(d.ctx)
	if conn == nil {
		return nil, false, context.Canceled
	}
	defer d.dbpool.Put(conn)

	toDeliver := make(map[int64]deliveryData) // stagingID -> delivery data

	const limit = 300
	// TODO: consider the ordering of messages. LIFO, FIFO?
	// Definitely process all local deliveries first.
	stmt := conn.Prep("SELECT StagingID, Recipient FROM MsgRecipients WHERE DeliveryState = $deliverySending ORDER BY StagingID LIMIT $limit;")
	stmt.SetInt64("$deliverySending", int64(db.DeliverySending))
	stmt.SetInt64("$limit", limit)
	count := 0
	for {
		if hasNext, err := stmt.Step(); err != nil {
			return nil, false, err
		} else if !hasNext {
			break
		}
		stagingID := stmt.GetInt64("StagingID")
		d := toDeliver[stagingID]
		d.recipients = append(d.recipients, stmt.GetText("Recipient"))
		toDeliver[stagingID] = d
		count++
	}
	for stagingID := range toDeliver {
		b, err := conn.OpenBlob("", "MsgRaw", "Content", stagingID, false)
		if err != nil {
			return nil, false, err
		}
		f := d.filer.BufferFile(0)
		_, err = io.Copy(f, b)
		b.Close()
		if err != nil {
			f.Close()
			return nil, false, err
		}
		if _, err := f.Seek(0, 0); err != nil {
			f.Close()
			return nil, false, err
		}

		// It is very messy to be doing message modification here.
		// Do it earlier, in a Processor-like object for incoming
		// mail submission.
		signer, err := d.findSigner(conn, stagingID)
		if err != nil {
			return nil, false, err
		}
		if signer != nil {
			dst := d.filer.BufferFile(0)
			err := msgcleaver.Sign(d.filer, signer, dst, f)
			f.Close()
			if err != nil {
				dst.Close()
				return nil, false, err
			}
			if _, err := dst.Seek(0, 0); err != nil {
				dst.Close()
				return nil, false, fmt.Errorf("final dst seek: %v", err)
			}
			f = dst
		}

		d := toDeliver[stagingID]
		d.contents = f
		toDeliver[stagingID] = d
	}

	deliveries = make([]deliveryData, 0, len(toDeliver))
	stmt = conn.Prep("SELECT Sender FROM Msgs WHERE StagingID = $stagingID;")
	for stagingID, d := range toDeliver {
		d.stagingID = stagingID

		stmt.Reset()
		stmt.SetInt64("$stagingID", stagingID)
		d.from, err = sqlitex.ResultText(stmt)
		if err != nil {
			return nil, false, err
		}

		deliveries = append(deliveries, d)
	}
	return deliveries, count == limit, nil
}

func (d *Deliverer) findSigner(conn *sqlite.Conn, stagingID int64) (*dkim.Signer, error) {
	stmt := conn.Prep("SELECT Sender FROM Msgs WHERE StagingID = $stagingID;")
	stmt.SetInt64("$stagingID", stagingID)
	senderAddr, err := sqlitex.ResultText(stmt)
	if err != nil {
		return nil, err
	}
	i := strings.LastIndexByte(senderAddr, '@')
	if i == -1 || i == len(senderAddr)-1 {
		return nil, fmt.Errorf("signer: bad sender: %q", senderAddr)
	}
	domain := senderAddr[i+1:]

	stmt = conn.Prep("SELECT Selector, PrivateKey FROM DKIMRecords WHERE DomainName = $domain AND Current = TRUE;")
	stmt.SetText("$domain", domain)
	if hasNext, err := stmt.Step(); err != nil {
		return nil, err
	} else if !hasNext {
		return nil, nil
	}
	selector := stmt.GetText("Selector")
	key := []byte(stmt.GetText("PrivateKey"))
	stmt.Reset()

	signer, err := dkim.NewSigner(key)
	if err != nil {
		return nil, err
	}
	signer.Domain = domain
	signer.Selector = selector
	return signer, nil
}

func (d *Deliverer) Run() error {
	defer func() { close(d.done) }()

	ticker := time.NewTicker(2 * time.Second)
	for {
		select {
		case <-d.ctx.Done():
			return nil
		case <-d.newmsg:
		case <-ticker.C:
		}

		deliveries, more, err := d.collectToDeliver()
		if err != nil {
			if err == context.Canceled {
				return nil
			}
			return err
		}

		if more {
			// There are probably more messages ready to send.
			// Prime the pump for the next cycle.
			select {
			case d.newmsg <- struct{}{}:
			default:
			}
		}

		var wg sync.WaitGroup
		for _, data := range deliveries {
			wg.Add(1)
			go func(data deliveryData) {
				err := d.deliver(data.stagingID, data.from, data.recipients, data.contents)
				if err != nil {
					// TODO plumb logging
					log.Printf("deliver %v: %v", data.stagingID, err)
				}
				data.contents.Close()
				wg.Done()
			}(data)
		}
		wg.Wait()
	}
}
