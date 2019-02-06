package spillbox

import "bytes"

// normalizeAddr normalizes email addresses.
// The new address is written inline into the provided bytes.
//
// Some email hosts have features that automatically map an arbitrary
// number of addresses onto one. In particular for gmail:
//
//	joe.smith@googlemail.com -> joesmith@gmail.com
//	joesmith+hello@gmail.com -> joesmith@gmail.com
//
// (Details https://gmail.googleblog.com/2008/03/2-hidden-ways-to-get-more-from-your.html)
//
// This function maps email addresses onto a minimal normal form,
// which helps with contact matching.
func normalizeAddr(addr []byte) (norm []byte) {
	// Ignore bad email addresses.
	if len(addr) == 0 {
		return addr
	}
	i := bytes.IndexByte(addr, '@')
	if i == -1 || i == len(addr)-1 {
		return addr
	}

	user, domain := addr[:i], addr[i+1:]

	if domain[len(domain)-1] == '.' {
		domain = domain[:len(domain)-1]
	}
	domain = bytes.ToLower(domain)
	if d, hasAlias := domainAliases[string(domain)]; hasAlias {
		domain = d
	}

	details := domainDetails[string(domain)]
	if details.IgnoreUserDots {
		u := user[:0] // we are strictly shortening user
		for _, b := range user {
			switch b {
			case '+':
				break
			case '.':
				// ignore
			default:
				u = append(u, b)
			}
		}
		user = u
	}
	if details.Caseless {
		// TODO: investigate whether servers support caseless UTF-8, RFC6531.
		asciiLower(user)
	}

	addr = addr[:0]
	addr = append(addr, user...)
	addr = append(addr, '@')
	addr = append(addr, domain...)

	return addr
}

func asciiLower(data []byte) {
	for i, b := range data {
		if b >= 'A' && b <= 'Z' {
			data[i] = b + ('a' - 'A')
		}
	}
}

type domainDetail struct {
	Caseless       bool
	IgnoreUserDots bool
}

var domainDetails = map[string]domainDetail{
	"meetup.com":       domainDetail{Caseless: true},
	"yahoo.com":        domainDetail{Caseless: true},
	"hotmail.com":      domainDetail{Caseless: true},
	"aol.com":          domainDetail{Caseless: true},
	"msn.com":          domainDetail{Caseless: true},
	"outlook.com":      domainDetail{Caseless: true},
	"facebook.com":     domainDetail{Caseless: true},
	"live.com":         domainDetail{Caseless: true},
	"comcast.net":      domainDetail{Caseless: true},
	"earthlink.net":    domainDetail{Caseless: true},
	"gmail.com":        domainDetail{Caseless: true, IgnoreUserDots: true},
	"zentus.com":       domainDetail{Caseless: true, IgnoreUserDots: true},
	"googlegroups.com": domainDetail{Caseless: true, IgnoreUserDots: true},
}

var domainAliases = map[string][]byte{
	"googlemail.com": []byte("gmail.com"),
}
