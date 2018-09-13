package css

import (
	"math"
	"strconv"
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

func AppendValue(dst []byte, v *Value) []byte {
	switch v.Type {
	case ValueIdent:
		dst = appendEscapedString(dst, v.Value)
	case ValueFunction:
		panic("TODO")
	case ValueHash, ValueHashID:
		dst = append(dst, '#')
		dst = appendEscapedString(dst, v.Value)
	case ValueString:
		dst = append(dst, '"')
		dst = appendEscapedString(dst, v.Value)
		dst = append(dst, '"')
	case ValueURL:
		dst = append(dst, `url("`...)
		dst = appendEscapedString(dst, v.Value)
		dst = append(dst, `")`...)
	case ValueDelim:
		dst = appendEscapedString(dst, v.Value)
	case ValueNumber:
		f := math.Float64frombits(v.Data)
		dst = strconv.AppendFloat(dst, f, 'e', -1, 64)
	case ValueInteger:
		dst = strconv.AppendInt(dst, int64(v.Data), 10)
	case ValuePercentage:
		dst = strconv.AppendInt(dst, int64(v.Data), 10)
		dst = append(dst, '%')
	case ValueDimension:
		panic("TODO")
	case ValueUnicodeRange:
		panic("TODO")
	case ValueIncludeMatch:
		dst = append(dst, '~', '=')
	case ValueDashMatch:
		dst = append(dst, '|', '=')
	case ValuePrefixMatch:
		dst = append(dst, '^', '=')
	case ValueSuffixMatch:
		dst = append(dst, '$', '=')
	case ValueSubstringMatch:
		dst = append(dst, '*', '=')
	case ValueComma:
		dst = append(dst, ',')
	}
	return dst
}

func AppendDecl(dst []byte, d *Decl) []byte {
	dst = appendEscapedString(dst, d.Property)
	dst = append(dst, ':', ' ')
	for i := range d.Values {
		v := &d.Values[i]
		if i > 0 && v.Type != ValueComma {
			dst = append(dst, ' ')
		}
		dst = AppendValue(dst, v)
	}
	dst = append(dst, ';')
	return dst
}
