// Package processor processes new incoming mail.
//
// Tasks include:
//	- cleaning HTML
//	- downloading and embedding HTML assets
//	- determining spam
package processor

import (
	"context"
	"io"
	"log"
	"sync"
	"time"

	"crawshaw.io/iox"
	"crawshaw.io/iox/webfetch"
	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
	"spilled.ink/email"
	"spilled.ink/email/dkim"
	"spilled.ink/email/msgbuilder"
	"spilled.ink/email/msgcleaver"
	"spilled.ink/html/htmlembed"
	"spilled.ink/spilldb/db"
)

type Processor struct {
	ctx      context.Context
	cancelFn func()
	done     chan struct{}

	dbpool    *sqlitex.Pool
	filer     *iox.Filer
	dkim      *dkim.Verifier
	embed     *htmlembed.Embedder
	localSend func(stagingID int64)

	newmsg chan struct{}

	maxReadyDateMu sync.Mutex
	maxReadyDate   int64
}

func NewProcessor(dbpool *sqlitex.Pool, filer *iox.Filer, httpc *webfetch.Client, localSend func(stagingID int64)) *Processor {
	ctx, cancelFn := context.WithCancel(context.Background())
	return &Processor{
		ctx:      ctx,
		cancelFn: cancelFn,
		done:     make(chan struct{}),

		dbpool:    dbpool,
		filer:     filer,
		dkim:      &dkim.Verifier{},
		embed:     htmlembed.NewEmbedder(filer, httpc),
		localSend: localSend,

		newmsg: make(chan struct{}, 1),
	}
}

func (p *Processor) Process(stagingID int64) {
	// It is OK to drop messages here, they will be
	// picked up on the periodic DB scan.
	select {
	case p.newmsg <- struct{}{}:
	default:
	}
}

func (p *Processor) Shutdown(ctx context.Context) {
	p.cancelFn()
	<-p.done
}

func (p *Processor) Run() error {
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

		toProcess, more, err := p.collectToProcess()
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
		for _, stagingID := range toProcess {
			wg.Add(1)
			go func(stagingID int64) {
				defer wg.Done()
				err := p.process(stagingID)
				if err != nil {
					// TODO plumb logging
					log.Printf("process %v: %v", stagingID, err)
				}
			}(stagingID)
		}
		wg.Wait()
	}
}

func (p *Processor) loadMaxReadyDate() error {
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

func (p *Processor) collectToProcess() (toProcess []int64, more bool, err error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return nil, false, context.Canceled
	}
	defer p.dbpool.Put(conn)

	const limit = 8

	stmt := conn.Prep("SELECT DISTINCT StagingID FROM MsgRecipients WHERE DeliveryState = $deliveryState ORDER BY StagingID LIMIT $limit;")
	stmt.SetInt64("$deliveryState", int64(db.DeliveryToProcess))
	stmt.SetInt64("$limit", limit)

	for {
		if hasNext, err := stmt.Step(); err != nil {
			return nil, false, err
		} else if !hasNext {
			break
		}
		stagingID := stmt.GetInt64("StagingID")
		toProcess = append(toProcess, stagingID)
	}

	more = len(toProcess) == limit
	return toProcess, more, nil
}

func findBodyHTML(msg *email.Msg) *email.Part {
	for i := range msg.Parts {
		part := &msg.Parts[i]
		if part.IsBody && part.ContentType == "text/html" {
			return part
		}
	}
	return nil
}

func (p *Processor) process(stagingID int64) (err error) {
	log.Printf("processing staging message %d", stagingID)

	rawMsg, err := p.loadMsg(stagingID)
	if err != nil {
		return err
	}

	var dkimStatus string
	if err := p.dkim.Verify(p.ctx, rawMsg); err != nil {
		dkimStatus = err.Error()
	} else {
		dkimStatus = "PASS"
	}
	rawMsg.Seek(0, 0)

	msg, err := msgcleaver.Cleave(p.filer, rawMsg)
	if err != nil {
		return err
	}
	defer msg.Close()
	htmlPart := findBodyHTML(msg)

	if htmlPart != nil {
		html, err := p.embed.Embed(p.ctx, htmlPart.Content)
		if err != nil {
			html.HTML.Close()
			for _, asset := range html.Assets {
				asset.Bytes.Close()
			}
			return err
		}
		// After this point we don't clean up the buffers in
		// the html objects because we are going to transfer
		// them to htmlPart.
		// They will be closed as part of the msg.Close above.

		htmlPart.CompressedSize = 0
		htmlPart.IsCompressed = false
		htmlPart.ContentTransferEncoding = ""
		htmlPart.ContentTransferSize = 0
		htmlPart.ContentTransferLines = 0
		htmlPart.Content.Close()
		htmlPart.Content = html.HTML

		msg.EncodedSize = 0

		for _, asset := range html.Assets {
			if asset.LoadError != nil {
				if asset.Bytes != nil {
					asset.Bytes.Close()
				}
				log.Printf("processor: msg %d asset %q load failed: %v", stagingID, asset.URL, asset.LoadError)
				continue
			}
			part := email.Part{
				PartNum:     len(msg.Parts) + 1,
				Name:        asset.Name,
				ContentType: asset.ContentType,
				ContentID:   asset.CID,
				Content:     asset.Bytes,
			}
			msg.Parts = append(msg.Parts, part)
		}
	}

	builder := &msgbuilder.Builder{
		Filer:         p.filer,
		FillOutFields: true,
	}
	fullMsg := p.filer.BufferFile(0)
	defer fullMsg.Close()
	if err := builder.Build(fullMsg, msg); err != nil {
		return err
	}

	if err := p.processSave(stagingID, dkimStatus, fullMsg); err != nil {
		return err
	}

	if p.localSend != nil {
		p.localSend(stagingID)
	}

	return nil
}

func (p *Processor) processSave(stagingID int64, dkimStatus string, data email.Buffer) (err error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return context.Canceled
	}
	defer p.dbpool.Put(conn)
	defer sqlitex.Save(conn)(&err)

	// Start with UPDATE to upgrade the Tx to an IMMEDIATE lock.
	stmt := conn.Prep("UPDATE Msgs SET DKIM = $dkim WHERE StagingID = $stagingID;")
	stmt.SetInt64("$stagingID", stagingID)
	stmt.SetText("$dkim", dkimStatus)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	stmt = conn.Prep("INSERT INTO MsgFull (StagingID, Content) VALUES ($stagingID, $content);")
	stmt.SetInt64("$stagingID", stagingID)
	stmt.SetZeroBlob("$content", data.Size())
	if _, err := stmt.Step(); err != nil {
		return err
	}

	data.Seek(0, 0)
	blob, err := conn.OpenBlob("", "MsgFull", "Content", stagingID, true)
	if err != nil {
		return err
	}
	_, err = io.Copy(blob, data)
	if clErr := blob.Close(); err == nil {
		err = clErr
	}
	if err != nil {
		return err
	}

	stmt = conn.Prep("UPDATE MsgRecipients SET DeliveryState = $deliveryState WHERE StagingID = $stagingID;")
	stmt.SetInt64("$deliveryState", db.DeliveryReceived)
	stmt.SetInt64("$stagingID", stagingID)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	// ReadyDate must be monotonically increasing.
	// If UnixNano doesn't give us that, fake it.
	readyDate := time.Now().UnixNano()

	p.maxReadyDateMu.Lock()
	if readyDate > p.maxReadyDate {
		p.maxReadyDate = readyDate
	} else {
		p.maxReadyDate++
		readyDate = p.maxReadyDate
	}
	p.maxReadyDateMu.Unlock()

	stmt = conn.Prep("UPDATE Msgs SET ReadyDate = $readyDate WHERE StagingID = $stagingID;")
	stmt.SetInt64("$readyDate", readyDate)
	stmt.SetInt64("$stagingID", stagingID)
	if _, err := stmt.Step(); err != nil {
		return err
	}

	return nil
}

func (p *Processor) findHTML(conn *sqlite.Conn, stagingID int64) (blobID, partNum int64, isCompressed bool, err error) {
	stmt := conn.Prep(`SELECT BlobID, PartNum, IsCompressed
		FROM MsgParts
		WHERE StagingID = $stagingID AND IsBody <> 0 AND ContentType = "text/html";`)
	stmt.SetInt64("$stagingID", stagingID)
	if hasNext, err := stmt.Step(); err != nil {
		return 0, 0, false, err
	} else if !hasNext {
		return 0, 0, false, nil // no HTML in this message
	}
	blobID = stmt.GetInt64("BlobID")
	partNum = stmt.GetInt64("PartNum")
	isCompressed = stmt.GetInt64("IsCompressed") != 0
	stmt.Reset()
	return blobID, partNum, isCompressed, nil
}

func (p *Processor) loadMsg(stagingID int64) (rawMsg *iox.BufferFile, err error) {
	conn := p.dbpool.Get(p.ctx)
	if conn == nil {
		return nil, context.Canceled
	}
	defer p.dbpool.Put(conn)

	return db.LoadMsg(conn, p.filer, stagingID, true)
}
