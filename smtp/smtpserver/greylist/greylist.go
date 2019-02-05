// Package greylist implements a variant of SMTP greylisting.
//
// A popular implementation of greylisting is OpenBSD's spamd(8).
// Details of how it works are available in its man page:
// http://man.openbsd.org/spamd.
//
// More general details of the algorithm are available at
// https://www.greylisting.org/.
//
// This implementation is relatively heavy-weight.
// Instead of tarpitting the first greylist attempt,
// the message is analyzed to see if there are other signals
// that should allow the first message to pass through.
package greylist

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"crawshaw.io/iox"

	"spilled.ink/smtp/smtpserver"
)

var ErrNotFound = errors.New("greylist: IP-from-to tuple not found")

type DB interface {
	Get(ctx context.Context, remoteAddr, from, to string) (time.Time, error)
	Put(ctx context.Context, remoteAddr, from, to string) error
}

// Greylist provides an smtpserver.NewMessageFunc that implements greylisting.
//
// If the message passes analysis then ProcessMsg is called.
type Greylist struct {
	Filer      *iox.Filer
	ProcessMsg func(ctx context.Context, msg *RawMsg) error
	Whitelist  func(ctx context.Context, remoteAddr net.Addr, from []byte) (bool, error)
	Blacklist  func(ctx context.Context, remoteAddr net.Addr, from []byte) (bool, error)
	GreyDB     DB
}

func (gl *Greylist) NewMessage(ctx context.Context, remoteAddr net.Addr, from []byte, authToken uint64) (smtpserver.Msg, error) {
	msg := &greyMsg{
		ctx:    ctx,
		gl:     gl,
		rawMsg: new(RawMsg),
	}
	msg.buf = append(msg.buf, from...)
	msg.rawMsg.From = msg.buf[0:len(from):len(from)]
	msg.rawMsg.RemoteAddr = remoteAddr

	return msg, nil
}

// TODO: move to email package?
type RawMsg struct {
	RemoteAddr  net.Addr
	From        []byte
	Recipients  [][]byte
	Whitelist   bool
	Content     io.ReadCloser
	ContentSize int64
	// TODO: DKIM analysis, SPF results, etc
}

type greyMsg struct {
	ctx    context.Context
	gl     *Greylist
	f      *iox.BufferFile
	rawMsg *RawMsg
	buf    []byte
}

func (g *greyMsg) AddRecipient(addr []byte) (bool, error) {
	g.buf = append(g.buf, addr...)
	addr = g.buf[len(g.buf)-len(addr) : len(g.buf) : len(g.buf)]
	g.rawMsg.Recipients = append(g.rawMsg.Recipients, addr)
	return true, nil
}

func (g *greyMsg) Write(line []byte) error {
	if g.f == nil {
		g.f = g.gl.Filer.BufferFile(0)
	}
	n, err := g.f.Write(line)
	g.rawMsg.ContentSize += int64(n)
	return err
}

func (g *greyMsg) Cancel() {
	if g.f != nil {
		g.f.Close()
	}
}

func (g *greyMsg) allow() (bool, error) {
	if g.gl.Whitelist != nil {
		if is, err := g.gl.Whitelist(g.ctx, g.rawMsg.RemoteAddr, g.rawMsg.From); err != nil {
			return false, err
		} else if is {
			g.rawMsg.Whitelist = true
			return true, nil
		}
	}
	if g.gl.Blacklist != nil {
		if is, err := g.gl.Blacklist(g.ctx, g.rawMsg.RemoteAddr, g.rawMsg.From); err != nil {
			return false, err
		} else if is {
			return false, nil
		}
	}
	// TODO: g.gl.DB.Get / Put
	return true, nil
}

func (g *greyMsg) Close() error {
	defer func() {
		if g.f != nil {
			g.f.Close()
		}
	}()

	if _, err := g.f.Seek(0, 0); err != nil {
		return err
	}
	g.rawMsg.Content = g.f

	return g.gl.ProcessMsg(g.ctx, g.rawMsg)
}
