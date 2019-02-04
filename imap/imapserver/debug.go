package imapserver

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"
)

const debugLiteralWrite = 256 // number of bytes of the literal to write

// debugWriter writes a copy of an IMAP session.
// It skips over long literals.
//
// There is no buffering in debugWriter because the imapserver
// batches writes to it using the same bufio it uses to batch
// network communication.
type debugWriter struct {
	sessionID string
	logf      func(format string, v ...interface{}) // used to report failed writing

	mu         sync.Mutex
	writer     io.Writer
	client     *debugWriterDirectional
	server     *debugWriterDirectional
	lastPrefix string
}

func newDebugWriter(sessionID string, logf func(format string, v ...interface{}), writer io.Writer) *debugWriter {
	w := &debugWriter{
		sessionID: sessionID,
		logf:      logf,
		writer:    writer,
	}
	w.client = &debugWriterDirectional{
		w:      w,
		prefix: "C: ",
	}
	w.server = &debugWriterDirectional{
		w:      w,
		prefix: "S: ",
	}
	return w
}

type debugWriterDirectional struct {
	w       *debugWriter
	prefix  string
	litHead int
	litSkip int
}

func (w *debugWriterDirectional) literalDataFollows(n int) {
	w.w.mu.Lock()
	defer w.w.mu.Unlock()

	if n < debugLiteralWrite {
		return // write the whole literal
	}
	w.litHead = debugLiteralWrite / 2
	litTail := debugLiteralWrite / 2
	w.litSkip = n - w.litHead - litTail
}

func (w *debugWriterDirectional) Write(p []byte) (int, error) {
	w.w.mu.Lock()
	defer w.w.mu.Unlock()

	n := len(p)

	if w.litHead > 0 {
		head := p
		if len(head) > w.litHead {
			head = head[:w.litHead]
		}
		// TODO: prefix write head
		if !w.writeWithPrefix(head) {
			return n, nil
		}
		w.litHead -= len(head)
		p = p[len(head):]
		if w.litHead == 0 {
			fmt.Fprintf(w.w.writer, "\n%s... skipping %d bytes of literal ...\n", w.prefix, w.litSkip)
			w.w.lastPrefix = ""
		}
	}
	if w.litSkip > 0 {
		if len(p) < w.litSkip {
			w.litSkip -= len(p)
			return n, nil
		}
		p = p[w.litSkip:]
		w.litSkip = 0
	}

	w.writeWithPrefix(p)
	return n, nil
}

func (w *debugWriterDirectional) writeWithPrefix(p []byte) bool {
	if len(p) == 0 {
		return true
	}
	if w.w.lastPrefix != w.prefix {
		if !w.writePrefix() {
			return false
		}
	}
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i == -1 {
			break
		}
		if !w.write(p[:i+1]) {
			return false
		}
		p = p[i+1:]
		if len(p) == 0 {
			w.w.lastPrefix = "" // whoever comes next should write a prefix
			break
		}
		if !w.writePrefix() {
			return false
		}
	}
	if !w.write(p) {
		return false
	}
	return true
}

func (w *debugWriterDirectional) write(p []byte) bool {
	if _, err := w.w.writer.Write(p); err != nil {
		w.w.logf("session(%s): debugWriter failed: %v", w.w.sessionID, err)
		return false
	}
	return true
}

func (w *debugWriterDirectional) writePrefix() bool {
	w.w.lastPrefix = w.prefix
	b := make([]byte, 0, 32)
	b = time.Now().AppendFormat(b, "15:04:05.000 ")
	b = append(b, w.prefix...)
	if _, err := w.w.writer.Write(b); err != nil {
		w.w.logf("session(%s): debugWriter failed: %v", w.w.sessionID, err)
		return false
	}
	return true
}
