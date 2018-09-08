package css

import (
	"errors"
	"io"
	"unicode/utf8"
)

// TODO: independent tests of _SourceReader
// TODO: does this deserve its own package? is it generally interesting?

var _ErrMaxBufExceeded = errors.New("sourcereader: max buffer size exceeded")

// _SourceReader reads runes from an io.Reader.
// It provides methods designed for writing scanners.
type _SourceReader struct {
	err error
	src io.Reader
	buf []byte // buffer. len-off is avail bytes, cap never exceeded
	off int    // buf read offset

	line, col, n int

	recHasNULL  bool // set if '\0' was seen while recording
	replaceNULL bool
	lastRuneLen int // number of bytes of buf in the last read rune, or -1
	lastCol     int // value of col for the previous line
}

func _NewSourceReader(src io.Reader, maxBuf int) *_SourceReader {
	if maxBuf == 0 {
		maxBuf = 4096
	}
	return &_SourceReader{
		src: src,
		buf: make([]byte, 0, 4096),
	}
}

func (r *_SourceReader) fill() {
	// Slide unnecessary bytes to the beginning of the buffer to make space.
	slideOff := r.off
	if r.lastRuneLen > 0 {
		slideOff -= r.lastRuneLen // keep the last rune for unget
	}
	if slideOff > 0 {
		copy(r.buf, r.buf[slideOff:])
		r.buf = r.buf[:len(r.buf)-slideOff]
		r.off -= slideOff
	}

	if r.off == cap(r.buf) {
		r.err = _ErrMaxBufExceeded // no space to fill
		return
	}

	allbuf := r.buf[0:cap(r.buf)]
	n, err := r.src.Read(allbuf[len(r.buf):])
	r.buf = allbuf[:len(r.buf)+n]
	if err != nil {
		r.err = err
	} else if n == 0 {
		r.err = io.ErrNoProgress
	}
}

// SetReplaceNULL configures the _SourceReader to replace any '\0' runes with
// the Unicode replacement character '\uFFFD'.
func (r *_SourceReader) SetReplaceNULL(v bool) {
	r.replaceNULL = v
}

func (r *_SourceReader) Error() error {
	return r.err
}

func (r *_SourceReader) peek() (rn rune, size int) {
	r.fillTo(0)
	if r.off >= len(r.buf) {
		return -1, 0
	}

	size = 1
	rn = rune(r.buf[r.off])
	if rn >= utf8.RuneSelf {
		rn, size = utf8.DecodeRune(r.buf[r.off:])
	}

	if r.replaceNULL && rn == 0 {
		rn = '\uFFFD' // unicode replacement character
	}

	return rn, size
}

func (r *_SourceReader) PeekRune() rune {
	rn, _ := r.peek()
	return rn
}

func (r *_SourceReader) fillTo(peekOff int) {
	for r.err == nil {
		if r.off+peekOff+utf8.UTFMax <= len(r.buf) {
			break
		}
		if r.off+peekOff < len(r.buf) && utf8.FullRune(r.buf[r.off+peekOff:]) {
			break
		}
		r.fill()
	}
}

func (r *_SourceReader) PeekRunes(runes []rune) error {
	peekOff := 0
	for i := range runes {
		r.fillTo(peekOff)
		off := r.off + peekOff
		if off >= len(r.buf) {
			for i < len(runes) {
				runes[i] = -1
				i++
			}
			return r.err
		}

		size := 1
		rn := rune(r.buf[off])
		if rn >= utf8.RuneSelf {
			rn, size = utf8.DecodeRune(r.buf[off:])
		}
		if r.replaceNULL && rn == 0 {
			rn = '\uFFFD' // unicode replacement character
		}
		runes[i] = rn
		peekOff += size
	}
	return nil
}

// GetRune reads a single UTF-8 encoded character.
// If an I/O error occurs reading, ReadRune returns -1.
// The error is available from the Error method.
func (r *_SourceReader) GetRune() rune {
	rn, size := r.peek()
	//println(fmt.Sprintf("GetRune rn=%s, size=%d", string(rn), size))

	if rn == -1 {
		return -1
	}

	r.lastRuneLen = size
	r.off += size

	if r.replaceNULL && rn == '\uFFFD' && size == 1 {
		r.recHasNULL = true
	}

	if rn == '\n' {
		r.lastCol = r.col
		r.line++
		r.col = 0
	} else {
		r.col += size
	}
	r.n += size

	return rn
}

// UngetRune unreads the last rune.
// Only a single rune can be unread.
func (r *_SourceReader) UngetRune() {
	if r.lastRuneLen < 0 {
		r.err = errors.New("sourcereader: no rune to unread")
		return
	}
	r.off -= r.lastRuneLen
	r.n -= r.lastRuneLen
	if r.col == 0 {
		r.col = r.lastCol
		r.line--
	} else {
		r.col -= r.lastRuneLen
	}
	r.lastRuneLen = -1
}

// Pos reports the line/column position and total bytes of the last read rune.
// Column is a byte offset from the last '\n'.
func (r *_SourceReader) Pos() (line, col, n int) {
	return r.line, r.col, r.n
}
