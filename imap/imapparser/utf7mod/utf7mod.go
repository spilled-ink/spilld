// Package utf7mod implements "Modified UTF-7".
//
// Modified UTF-7 is described in RFC 3501 section 5.1.3,
// based on the original UTF-7 defined in RFC 2152.
//
// There are several MUST requirements in the spec that
// we relax for decoding. There are no good options when
// faced with bad UTF-7, so we make do as best we can.
package utf7mod

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

var ErrInvalidUTF7 = errors.New("utf7mod: invalid UTF-7")

const encodeModB64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+,"

// Modified UTF-7 uses a modified base64, described as:
//
//	modified BASE64, with a further modification from
//	[UTF-7] that "," is used instead of "/".
var b64 = base64.NewEncoding(encodeModB64).WithPadding(base64.NoPadding)

func AppendDecode(dst, src []byte) ([]byte, error) {
	for len(src) > 0 {
		c := src[0]
		src = src[1:]
		if c != '&' {
			dst = append(dst, c)
			continue
		}
		i := bytes.IndexByte(src, '-')
		if i == -1 {
			return nil, ErrInvalidUTF7
		}
		if i == 0 {
			src = src[1:]
			dst = append(dst, '&')
			continue
		}
		scratch := make([]byte, 0, 64)
		scratch = append(scratch, make([]byte, b64.DecodedLen(i))...)
		n, err := b64.Decode(scratch, src[:i])
		src = src[i+1:]
		if err != nil {
			return nil, fmt.Errorf("utf7mod: decode: %v", err)
		}
		scratch = scratch[:n]
		if len(scratch)%1 == 1 {
			return nil, ErrInvalidUTF7
		}
		for len(scratch) > 0 {
			r := rune(scratch[0])<<8 | rune(scratch[1])
			scratch = scratch[2:]
			if utf16.IsSurrogate(r) {
				if len(scratch) == 0 {
					return nil, ErrInvalidUTF7
				}
				r2 := rune(scratch[0])<<8 | rune(scratch[1])
				scratch = scratch[2:]
				r = utf16.DecodeRune(r, r2)
			}
			dst = appendRune(dst, r)
		}
	}
	return dst, nil
}

func appendRune(slice []byte, c rune) []byte {
	var b [4]byte
	return append(slice, b[:utf8.EncodeRune(b[:], c)]...)
}

func AppendEncode(dst, src []byte) ([]byte, error) {
	for len(src) > 0 {
		r, _ := utf8.DecodeRune(src)
		if r == '&' {
			dst = append(dst, '&', '-')
			src = src[1:]
			continue
		} else if r < utf8.RuneSelf {
			dst = append(dst, byte(r))
			src = src[1:]
			continue
		}
		// Encode a sequence of non-ASCII as base64-encoded utf16be.
		scratch := make([]byte, 0, 64)
		for len(src) > 0 {
			r, sz := utf8.DecodeRune(src)
			if r < utf8.RuneSelf {
				break
			}
			src = src[sz:]
			if r1, r2 := utf16.EncodeRune(r); r1 != '\uFFFD' {
				scratch = append(scratch, byte(r1>>8), byte(r1))
				r = r2
			}
			scratch = append(scratch, byte(r>>8), byte(r))
		}

		// Pad the UTF-16BE with zeros as per RFC2152.
		b64len := b64.EncodedLen(len(scratch))

		dst = append(dst, '&')
		dst = append(dst, make([]byte, b64len)...)
		b64.Encode(dst[len(dst)-b64len:], scratch)
		dst = append(dst, '-')
	}

	return dst, nil
}
