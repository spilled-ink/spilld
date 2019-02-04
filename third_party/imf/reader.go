// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package imf

// Originally from go/src/net/textproto/reader.go.

import (
	"bufio"
	"bytes"

	"spilled.ink/email"
)

// A Reader implements convenience methods for reading requests
// or responses from a text protocol network connection.
type Reader struct {
	R     *bufio.Reader
	buf   []byte // a re-usable buffer for readContinuedLineSlice
	nRead int    // bytes read from R
}

// NewReader returns a new Reader reading from r.
//
// To avoid denial of service attacks, the provided bufio.Reader
// should be reading from an io.LimitReader or similar Reader to bound
// the size of responses.
func NewReader(r *bufio.Reader) *Reader {
	return &Reader{R: r}
}

// NumRead returns the number of bytes read from the underlying
// buffered reader so far.
//
// It assumes that newlines are always \n, not \r\n.
func (r *Reader) NumRead() int { return r.nRead }

func (r *Reader) readLineSlice() ([]byte, error) {
	var line []byte
	for {
		l, more, err := r.R.ReadLine()
		if err != nil {
			return nil, err
		}
		r.nRead += len(l)
		if !more {
			r.nRead += 1 // assume never given \r\n
		}
		// Avoid the copy if the first call produced a full line.
		if line == nil && !more {
			return l, nil
		}
		line = append(line, l...)
		if !more {
			break
		}
	}
	return line, nil
}

func (r *Reader) readContinuedLineSlice() ([]byte, error) {
	// Read the first line.
	line, err := r.readLineSlice()
	if err != nil {
		return nil, err
	}
	if len(line) == 0 { // blank line - no continuation
		return line, nil
	}

	// Optimistically assume that we have started to buffer the next line
	// and it starts with an ASCII letter (the next header key), so we can
	// avoid copying that buffered data around in memory and skipping over
	// non-existent whitespace.
	if r.R.Buffered() > 1 {
		peek, err := r.R.Peek(1)
		if err == nil && isASCIILetter(peek[0]) {
			return trim(line), nil
		}
	}

	// ReadByte or the next readLineSlice will flush the read buffer;
	// copy the slice into buf.
	r.buf = append(r.buf[:0], trim(line)...)

	// Read continuation lines.
	for r.skipSpace() > 0 {
		line, err := r.readLineSlice()
		if err != nil {
			break
		}
		r.buf = append(r.buf, ' ')
		r.buf = append(r.buf, trim(line)...)
	}
	return r.buf, nil
}

// skipSpace skips R over all spaces and returns the number of bytes skipped.
func (r *Reader) skipSpace() int {
	n := 0
	for {
		c, err := r.R.ReadByte()
		if err != nil {
			// Bufio will keep err until next read.
			break
		}
		if c != ' ' && c != '\t' {
			r.R.UnreadByte()
			break
		}
		n++
	}
	r.nRead += n
	return n
}

// ReadMIMEHeader reads a MIME-style header from r.
// The header is a sequence of possibly continued Key: Value lines
// ending in a blank line.
// The returned map m maps email.CanonicalKey(key) to a
// sequence of values in the same order encountered in the input.
//
// For example, consider this input:
//
//	My-Key: Value 1
//	Long-Key: Even
//	       Longer Value
//	My-Key: Value 2
//
// Given that input, ReadMIMEHeader returns the map:
//
//	map[string][]string{
//		"My-Key": {"Value 1", "Value 2"},
//		"Long-Key": {"Even Longer Value"},
//	}
//
func (r *Reader) ReadMIMEHeader() (email.Header, error) {
	// Avoid lots of small slice allocations later by allocating one
	// large one ahead of time which we'll cut up into smaller
	// slices. If this isn't big enough later, we allocate small ones.
	var strs [][]byte
	hint := r.upcomingHeaderNewlines()
	if hint > 0 {
		strs = make([][]byte, hint)
	}

	m := email.Header{
		Index: make(map[email.Key][][]byte),
	}

	// The first line cannot start with a leading space.
	if buf, err := r.R.Peek(1); err == nil && (buf[0] == ' ' || buf[0] == '\t') {
		line, err := r.readLineSlice()
		if err != nil {
			return m, err
		}
		return m, ProtocolError("malformed MIME header initial line: " + string(line))
	}

	var valErr error
	for {
		kv, err := r.readContinuedLineSlice()
		if len(kv) == 0 {
			if err == nil {
				err = valErr
			}
			return m, err
		}

		// Key ends at first colon; should not have trailing spaces
		// but they appear in the wild, violating specs, so we remove
		// them if present.
		i := bytes.IndexByte(kv, ':')
		if i < 0 {
			return m, ProtocolError("malformed MIME header line: " + string(kv))
		}
		endKey := i
		for endKey > 0 && kv[endKey-1] == ' ' {
			endKey--
		}
		key := email.CanonicalKey(kv[:endKey])

		// As per RFC 7230 field-name is a token, tokens consist of one or more chars.
		// We could return a ProtocolError here, but better to be liberal in what we
		// accept, so if we get an empty key, skip it.
		if key == "" {
			continue
		}

		// Skip initial spaces in value.
		i++ // skip colon
		for i < len(kv) && (kv[i] == ' ' || kv[i] == '\t') {
			i++
		}
		value := kv[i:]
		if bytes.Index(value, []byte("=?")) >= 0 {
			var valueStr string
			valueStr, err = mimeDecoder.DecodeHeader(string(value))
			value = []byte(valueStr)
		} else {
			// TODO: this should be unnecessary. Remove when the bugs are ironed out
			vcopy := make([]byte, len(value))
			copy(vcopy, value)
			value = vcopy
		}
		if err != nil {
			if valErr == nil {
				valErr = err
			}
			// A bad value is enough reason to throw away the particular
			// header, but not enough reason to stop processing headers.
			continue
		}

		vv := m.Index[key]
		if vv == nil && len(strs) > 0 {
			// More than likely this will be a single-element key.
			// Most headers aren't multi-valued.
			// Set the capacity on strs[0] to 1, so any future append
			// won't extend the slice into the other strings.
			vv, strs = strs[:1:1], strs[1:]
			vv[0] = value
			m.Index[key] = vv
		} else {
			m.Index[key] = append(vv, value)
		}
		m.Entries = append(m.Entries, email.HeaderEntry{
			Key:   key,
			Value: value,
		})

		if err != nil {
			return m, err
		}
	}
}

// upcomingHeaderNewlines returns an approximation of the number of newlines
// that will be in this header. If it gets confused, it returns 0.
func (r *Reader) upcomingHeaderNewlines() (n int) {
	// Try to determine the 'hint' size.
	r.R.Peek(1) // force a buffer load if empty
	s := r.R.Buffered()
	if s == 0 {
		return
	}
	peek, _ := r.R.Peek(s)
	for len(peek) > 0 {
		i := bytes.IndexByte(peek, '\n')
		if i < 3 {
			// Not present (-1) or found within the next few bytes,
			// implying we're at the end ("\r\n\r\n" or "\n\n")
			return
		}
		n++
		peek = peek[i+1:]
	}
	return
}
