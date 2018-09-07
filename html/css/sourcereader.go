package css

import (
	"errors"
	"io"
	"unicode/utf8"
)

var ErrMaxBufExceeded = errors.New("sourcereader: max buffer size exceeded")

type SourceReader struct {
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

func NewSourceReader(src io.Reader, maxBuf int) *SourceReader {
	if maxBuf == 0 {
		maxBuf = 4096
	}
	return &SourceReader{
		src: src,
		buf: make([]byte, 0, 4096),
	}
}

func (r *SourceReader) fill() {
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
		r.err = ErrMaxBufExceeded // no space to fill
		return
	}

	allbuf := r.buf[0:cap(r.buf)]
	n, err := r.src.Read(allbuf[r.off:])
	r.buf = allbuf[:len(r.buf)+n]
	if err != nil {
		r.err = err
	} else if n == 0 {
		r.err = io.ErrNoProgress
	}
}

// SetReplaceNULL configures the SourceReader to replace any '\0' runes with
// the Unicode replacement character '\uFFFD'.
func (r *SourceReader) SetReplaceNULL(v bool) {
	r.replaceNULL = v
}

func (r *SourceReader) Error() error {
	return r.err
}

func (r *SourceReader) peek() (rn rune, size int) {
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

func (r *SourceReader) PeekRune() rune {
	rn, _ := r.peek()
	return rn
}

func (r *SourceReader) fillTo(peekOff int) {
	for r.off+peekOff+utf8.UTFMax > len(r.buf) && !utf8.FullRune(r.buf[r.off+peekOff:]) && r.err == nil {
		r.fill()
	}
}

func (r *SourceReader) PeekRunes(runes []rune) error {
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
func (r *SourceReader) GetRune() rune {
	rn, size := r.peek()
	//println(fmt.Sprintf("GetRune rn=%s, size=%d", string(rn), size))

	r.lastRuneLen = -1
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
func (r *SourceReader) UngetRune() {
	if r.lastRuneLen < 0 {
		r.err = errors.New("sourcereader: no rune to unread")
		return
	}
	r.off -= r.lastRuneLen
	r.n -= r.lastRuneLen
	if r.col == 0 {
		r.col = r.lastCol
	}
	r.line--
	r.lastRuneLen = -1
}

// Pos reports the line/column position and total bytes of the last read rune.
// Column is a byte offset from the last '\n'.
func (r *SourceReader) Pos() (line, col, n int) {
	return r.line, r.col, r.n
}
