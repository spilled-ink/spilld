package msgcleaver

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"strings"

	"crawshaw.io/iox"
	"spilled.ink/email"
	"spilled.ink/email/dkim"
	"spilled.ink/email/msgbuilder"
	"spilled.ink/third_party/imf"
)

func Cleave(filer *iox.Filer, src io.Reader) (*email.Msg, error) {
	// Split the input into parts.
	msg, err := cleave(filer, src)
	if err != nil {
		return nil, fmt.Errorf("msgcleaver: %v", err)
	}

	// Re-encode the parts to compute the body structure fields.
	// This is not cheap, but this work is largely unavoidable:
	// there is no obvious way to calculate the size of a
	// quoted-printable
	builder := msgbuilder.Builder{
		Filer:         filer,
		FillOutFields: true,
	}
	lw := new(lengthWriter)
	if err := builder.Build(lw, msg); err != nil {
		msg.Close()
		return nil, fmt.Errorf("msgcleaver: %v", err)
	}
	msg.EncodedSize = lw.n // TODO: move this into msgbuilder?
	for i := range msg.Parts {
		msg.Parts[i].Content.Seek(0, 0)
	}

	return msg, nil
}

func Sign(filer *iox.Filer, signer *dkim.Signer, dst io.Writer, src io.Reader) error {
	msg, err := cleave(filer, src)
	if err != nil {
		return fmt.Errorf("msgcleaver: %v", err)
	}
	builder := msgbuilder.Builder{
		Filer:         filer,
		FillOutFields: true,
		DKIM:          signer,
	}
	err = builder.Build(dst, msg)
	msg.Close()
	if err != nil {
		return fmt.Errorf("msgcleaver: %v", err)
	}
	return nil
}

func cleave(filer *iox.Filer, src io.Reader) (msgPtr *email.Msg, err error) {
	msg := new(email.Msg)
	defer func() {
		if err != nil {
			msg.Close()
		}
	}()

	h := sha256.New()
	r := bufio.NewReader(io.TeeReader(src, h))

	imfr := imf.NewReader(r)
	msg.Headers, err = imfr.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}

	processPartFn := func(hdr email.Header, parentMediaType string, localPartNum int, r io.Reader) (err error) {
		var buf *iox.BufferFile
		defer func() {
			if err != nil && buf != nil {
				buf.Close()
			}
		}()

		mediaType, params, err := mime.ParseMediaType(string(hdr.Get("Content-Type")))
		if err != nil {
			return err
		}

		switch strings.ToLower(string(hdr.Get("Content-Transfer-Encoding"))) {
		case "base64":
			r = base64.NewDecoder(base64.StdEncoding, r)
		case "quoted-printable":
			r = quotedprintable.NewReader(r)
		}

		isAttachment := false
		fileName := ""
		if d, dparams, err := mime.ParseMediaType(string(hdr.Get("Content-Disposition"))); err == nil {
			fileName = dparams["filename"]
			if strings.EqualFold(d, "attachment") {
				isAttachment = true
			}
		}
		if fileName == "" {
			fileName = params["name"]
		}

		isBody := false
		switch parentMediaType {
		case "":
			if !strings.HasPrefix(mediaType, "multipart/") {
				isBody = true
			}
		case "multipart/alternative":
			isBody = true
		case "multipart/mixed":
			// TODO this is wrong.
			// If for any localPartNum Content-Disposition: inline, then isBody = true
			isBody = localPartNum == 0
			if len(hdr.Get("Content-Disposition")) == 0 {
				// We have to decide if this is an attachment.
				isAttachment = localPartNum > 0
			}
		case "multipart/related":
			isBody = localPartNum == 0
		}

		contentID := strings.TrimSuffix(strings.TrimPrefix(string(hdr.Get("Content-ID")), "<"), ">")

		buf = filer.BufferFile(0)
		if mediaType == "text/html" && isBody {
			// TODO consider cleaning HTML
			_, err = io.Copy(buf, r)
		} else {
			_, err = io.Copy(buf, r)
		}
		if err != nil {
			return err
		}
		if _, err := buf.Seek(0, 0); err != nil {
			return err
		}

		if mediaType == "image/jpg" { // yes people do this
			mediaType = "image/jpeg"
		}

		var compressedSize int64
		compress := true
		switch mediaType {
		case "image/jpeg", "image/png", "image/gif",
			"application/zip", "application/gzip",
			"application/x-gtar", "application/x-rar-compressed":
			compress = false // do not compress the uncompressable
		default:
			if buf.Size() < 1<<15 {
				compress = false // do not compress small parts
			}
		}
		// Compress the content into /dev/null to measure the
		// size savings, if any, and have a size to record if
		// we choose to store it compressed.
		if compress {
			lw := new(lengthWriter)
			gzw := gzip.NewWriter(lw)
			if _, err := io.Copy(gzw, buf); err != nil {
				return err
			}
			if err := gzw.Close(); err != nil {
				return err
			}
			compressedSize = lw.n
			compress = float64(lw.n)/float64(buf.Size()) < 0.9
			if _, err := buf.Seek(0, 0); err != nil {
				return err
			}
		}

		p := email.Part{
			PartNum:        len(msg.Parts),
			Name:           fileName,
			IsBody:         isBody,
			IsAttachment:   isAttachment,
			IsCompressed:   compress,
			CompressedSize: compressedSize,
			ContentType:    mediaType,
			ContentID:      contentID,
			Content:        buf,
		}
		msg.Parts = append(msg.Parts, p)

		return nil
	}
	if err := walkMime(msg.Headers, processPartFn, r); err != nil {
		return nil, fmt.Errorf("cannot process mime part: %v", err)
	}

	hash := h.Sum(make([]byte, 0, sha256.Size))
	msg.Seed = int64(binary.LittleEndian.Uint64(hash))
	msg.RawHash = base64.StdEncoding.EncodeToString(hash)

	return msg, nil
}

func walkMime(hdr email.Header, fn func(hdr email.Header, parentMediaType string, localPartNum int, r io.Reader) error, r io.Reader) error {
	return walkMimeRec(hdr, fn, "", 0, r)
}

func walkMimeRec(hdr email.Header, fn func(hdr email.Header, parentMediaType string, localPartNum int, r io.Reader) error, parentMediaType string, localPartNum int, r io.Reader) error {
	mediaType, params, err := mime.ParseMediaType(string(hdr.Get("Content-Type")))
	if err != nil {
		return fn(hdr, parentMediaType, 0, r)
	}

	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		mr := imf.NewMultipartReader(r, params["boundary"])
		for i := 0; ; i++ {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				// TODO: handle this. just fill out plain text?
				return fmt.Errorf("walkMime: corrupt mime part: %v", err)
			}
			if err := walkMimeRec(part.Header, fn, mediaType, i, part); err != nil {
				return err
			}
		}
		return nil
	} else {
		return fn(hdr, parentMediaType, localPartNum, r)
	}
}

type lengthWriter struct{ n int64 }

func (w *lengthWriter) Write(p []byte) (n int, err error) {
	w.n += int64(len(p))
	return len(p), nil
}
