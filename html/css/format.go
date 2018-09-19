package css

import (
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
		dst = appendEscapedString(dst, v.Value)
		dst = append(dst, '(')
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
	case ValueNumber, ValueInteger:
		dst = strconv.AppendFloat(dst, v.Number, 'f', -1, 64)
	case ValuePercentage:
		dst = strconv.AppendFloat(dst, v.Number, 'f', -1, 64)
		dst = append(dst, '%')
	case ValueDimension:
		dst = strconv.AppendFloat(dst, v.Number, 'f', -1, 64)
		dst = append(dst, v.Value...)
	case ValueUnicodeRange:
		dst = append(dst, v.Value...)
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
		if i > 0 {
			switch v.Type {
			case ValueComma, ValueFunction, ValueDelim:
			default:
				switch d.Values[i-1].Type {
				case ValueFunction:
				default:
					dst = append(dst, ' ')
				}
			}
		}
		dst = AppendValue(dst, v)
	}
	dst = append(dst, ';')
	return dst
}
