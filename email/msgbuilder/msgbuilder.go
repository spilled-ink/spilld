package msgbuilder

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"

	"crawshaw.io/iox"
	"spilled.ink/email"
	"spilled.ink/email/dkim"
)

type Builder struct {
	DKIM          *dkim.Signer
	Filer         *iox.Filer
	FillOutFields bool // fill out Part encoding and size fields
}

// Build builds the MIME-encoded text form of msg.
// It rewrites msg.Headers as necessary.
func (b *Builder) Build(w io.Writer, msg *email.Msg) error {
	if err := b.write(w, msg); err != nil {
		return fmt.Errorf("msgbuilder.Build: %v", err)
	}
	return nil
}

func (b *Builder) write(w io.Writer, msg *email.Msg) error {
	root, err := BuildTree(msg)
	if err != nil {
		return err
	}

	body := b.Filer.BufferFile(0)
	defer body.Close()
	if err := b.WriteNode(body, root); err != nil {
		return err
	}

	// Remove headers we will rewrite.
	hdr := &msg.Headers
	hdr.Del("MIME-Version")
	hdr.Add("MIME-Version", []byte("1.0"))
	root.Header.ForEach(func(key email.Key, val string) {
		hdr.Del(key)
		if val != "" {
			hdr.Add(key, []byte(val))
		}
	})

	if _, err := body.Seek(0, 0); err != nil {
		return err
	}

	// TODO: in relay forwarding, a message can have multiple
	// DKIM-Signatures. Do any of those cases apply here?
	if b.DKIM != nil && len(hdr.Get("DKIM-Signature")) == 0 {
		sig, err := b.DKIM.Sign(stringHeaders{hdr}, bufio.NewReader(body))
		if err != nil {
			return err
		}
		hdr.Add("DKIM-Signature", sig)
		if _, err := body.Seek(0, 0); err != nil {
			return err
		}
	}

	if _, err := msg.Headers.Encode(w); err != nil {
		return err
	}
	if _, err := io.Copy(w, body); err != nil {
		return err
	}

	return nil
}

func (b *Builder) WriteNode(w io.Writer, node *TreeNode) error {
	if node.Part != nil {
		return b.writePart(w, node.Header, node.Part)
	}

	// TODO: write a better version of ParseMediaType
	_, params, err := mime.ParseMediaType(node.Header.ContentType)
	if err != nil {
		return err
	}
	boundary := params["boundary"]

	mw := multipart.NewWriter(w)
	if err := mw.SetBoundary(boundary); err != nil {
		panic(err)
	}

	for _, kid := range node.Kids {
		tphdr := make(textproto.MIMEHeader)
		kid.Header.ForEach(func(key email.Key, val string) {
			if val != "" {
				tphdr.Add(string(key), val)
			}
		})
		w, err := mw.CreatePart(tphdr)
		if err != nil {
			return err
		}
		if err := b.WriteNode(w, &kid); err != nil {
			return err
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}

	return nil
}

func (b *Builder) writePart(w io.Writer, hdr PartHeader, part *email.Part) error {
	lenW := new(lengthWriter)
	w = io.MultiWriter(w, lenW)

	if err := EncodeContent(w, hdr, part); err != nil {
		return err
	}

	if b.FillOutFields {
		part.ContentTransferEncoding = hdr.ContentTransferEncoding
		part.ContentTransferSize = lenW.n
		part.ContentTransferLines = lenW.lines + 1
	}

	return nil
}

func EncodeContent(w io.Writer, hdr PartHeader, part *email.Part) error {
	if part.Content == nil {
		return fmt.Errorf("msgbuilder.EncodeContent: part %d has no content", part.PartNum)
	}
	if _, err := part.Content.Seek(0, 0); err != nil {
		return fmt.Errorf("msgbuilder.EncodeContent: part %d seek failed: %v", part.PartNum, err)
	}

	switch hdr.ContentTransferEncoding {
	case "", "7bit":
		if _, err := io.Copy(w, part.Content); err != nil {
			return err
		}
	case "quoted-printable":
		qpw := quotedprintable.NewWriter(w)
		if _, err := io.Copy(qpw, part.Content); err != nil {
			return err
		}
		if err := qpw.Close(); err != nil {
			return err
		}
	case "base64":
		w = &lineBreakWriter{w: w, breakAt: 68}
		b64w := base64.NewEncoder(base64.StdEncoding, w)
		if _, err := io.Copy(b64w, part.Content); err != nil {
			return err
		}
		if err := b64w.Close(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("msgbuilder: unknown content-transfer-encoding: %q", hdr.ContentTransferEncoding)
	}
	part.Content.Seek(0, 0)
	return nil
}

func randBoundary(rnd *rand.Rand) string {
	var buf [12]byte
	_, err := io.ReadFull(rnd, buf[:])
	if err != nil {
		panic(err)
	}
	// '.' and '.' are valid boundary bytes but not valid base64 bytes,
	// so including them provides trivial separation from all base64
	// content, which is how all tricky content is encoded.
	return "." + base64.StdEncoding.EncodeToString(buf[:]) + "."
}

type stringHeaders struct {
	hdr *email.Header
}

func (s stringHeaders) Get(name string) string {
	return string(s.hdr.Get(email.CanonicalKey([]byte(name))))
}

type lengthWriter struct {
	n     int64
	lines int64
}

func (w *lengthWriter) Write(p []byte) (n int, err error) {
	w.n += int64(len(p))
	for _, b := range p {
		if b == '\n' {
			w.lines++
		}
	}
	return len(p), nil
}

type lineBreakWriter struct {
	w       io.Writer
	breakAt int
	seen    int
}

func (w *lineBreakWriter) Write(p []byte) (n int, err error) {
	for len(p) > 0 {
		if w.seen == w.breakAt {
			n2, err := w.w.Write(crlf)
			n += n2
			if err != nil {
				return n, err
			}
			w.seen = 0
		}

		toWrite := len(p)
		if toWrite-w.seen > w.breakAt {
			toWrite = w.breakAt - w.seen
		}
		n2, err := w.w.Write(p[:toWrite])
		n += n2
		w.seen += n2
		p = p[n2:]
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

var crlf = []byte{'\r', '\n'}
