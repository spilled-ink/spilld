package css

import (
	"bytes"
)

func appendEscapedString(dst, src []byte) []byte {
	for _, c := range src {
		switch c {
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', '\n')
		case '"':
			dst = append(dst, '\\', '"')
		default:
			dst = append(dst, c)
		}
	}
	return dst
}

func FormatRaw(d *Decl) {
	d.PropertyRaw = appendEscapedString(d.PropertyRaw[:0], d.Property)
	for i := range d.Values {
		v := &d.Values[i]
		switch v.Type {
		case ValueIdent:
			v.Raw = appendEscapedString(v.Raw[:0], v.Value)
		case ValueFunction:
			panic("TODO")
		case ValueHash, ValueHashID:
			v.Raw = append(v.Raw[:0], '#')
			v.Raw = appendEscapedString(v.Raw, v.Value)
		case ValueString:
			v.Raw = append(v.Raw[:0], '"')
			v.Raw = appendEscapedString(v.Raw, v.Value)
			v.Raw = append(v.Raw, '"')
		case ValueURL:
			v.Raw = append(v.Raw[:0], `url("`...)
			v.Raw = appendEscapedString(v.Raw, v.Value)
			v.Raw = append(v.Raw, `")`...)
		case ValueDelim:
		case ValueNumber:
		case ValueInteger:
		case ValuePercentage:
			panic("TODO")
		case ValueDimension:
			panic("TODO")
		case ValueUnicodeRange:
			panic("TODO")
		case ValueIncludeMatch:
			v.Raw = append(v.Raw[:0], '~', '=')
		case ValueDashMatch:
			v.Raw = append(v.Raw[:0], '|', '=')
		case ValuePrefixMatch:
			v.Raw = append(v.Raw[:0], '^', '=')
		case ValueSuffixMatch:
			v.Raw = append(v.Raw[:0], '$', '=')
		case ValueSubstringMatch:
			v.Raw = append(v.Raw[:0], '*', '=')
		case ValueComma:
			v.Raw = append(v.Raw[:0], ',')
		}
	}
}

func FormatDecl(dst *bytes.Buffer, d *Decl) {
	FormatRaw(d)
	dst.Write(d.PropertyRaw)
	dst.WriteString(": ")
	for i, val := range d.Values {
		if i > 0 && val.Type != ValueComma {
			dst.WriteByte(' ')
		}
		dst.Write(val.Raw)
	}
	dst.WriteByte(';')
}
