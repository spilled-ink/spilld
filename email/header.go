package email

import (
	"bytes"
	"fmt"
	"io"
)

// Key is a canonical MIME header entry key.
//
// Use CanonicalKey to canonise bytes as a Key.
type Key string

type HeaderEntry struct {
	Key   Key
	Value []byte
}

func (entry *HeaderEntry) Encode(w io.Writer) (n int, err error) {
	var wErr error
	defer func() {
		if err == nil {
			err = wErr
		}
	}()
	printf := func(format string, args ...interface{}) {
		var n2 int
		n2, err := fmt.Fprintf(w, format, args...)
		if wErr == nil {
			wErr = err
		}
		n += n2
	}

	v := entry.Value
	if len(v) == 0 {
		printf("%s:\r\n", entry.Key)
		return 0, nil
	}
	printf("%s: ", entry.Key)

	// Header line limit:
	//
	// 	Each line of characters MUST be no more than 998 characters, and
	//	SHOULD be no more than 78 characters, excluding	the CRLF.
	//
	// https://tools.ietf.org/html/rfc5322#section-2.1.1
	//
	// We aim for conservative lines.
	// If we cannot manage that, we enforce the header limit.
	const padding = "    "
	spent := len(entry.Key) - len(": ")
	limit := 78

	firstPass := false
	for {
		if len(v) < limit-spent {
			printf("%s", v)
			break
		}
		var i int
		for i = limit - spent - 1; i > 0; i-- {
			if v[i] == ' ' {
				break
			}
		}
		if i == 0 {
			// There is nowhere to break this line.
			if limit == 78 {
				limit = 998
				continue
			}
			// RFC 5322 says we MUST not exceed this, so we do not.
			// Insert folding white space so we can break.
			i = 998 - spent
		}
		if firstPass {
			printf("%s", v[:i])
			firstPass = false
		} else {
			printf("%s\r\n%s", v[:i], padding)
		}
		spent = len(padding)
		limit = 78
		v = v[i:]
	}
	printf("\r\n")
	return n, nil
}

// Header is a MIME-style header.
type Header struct {
	Entries []HeaderEntry
	Index   map[Key][][]byte
}

func (h *Header) Add(k Key, v []byte) {
	h.Entries = append(h.Entries, HeaderEntry{Key: k, Value: v})
	if h.Index == nil {
		h.Index = make(map[Key][][]byte)
	}
	h.Index[k] = append(h.Index[k], v)
}

func (h *Header) Get(k Key) []byte {
	if h.Index == nil {
		h.Index = make(map[Key][][]byte)
		for _, entry := range h.Entries {
			h.Index[entry.Key] = append(h.Index[entry.Key], entry.Value)
		}
	}
	vals := h.Index[k]
	if len(vals) == 0 {
		return nil
	}
	return vals[0]
}

func (h *Header) Del(k Key) {
	var e []HeaderEntry
	for _, entry := range h.Entries {
		if entry.Key != k {
			e = append(e, entry)
		}
	}
	h.Entries = e
	if h.Index != nil {
		delete(h.Index, k)
	}
}

func (h *Header) Encode(w io.Writer) (n int, err error) {
	for _, entry := range h.Entries {
		n2, err := entry.Encode(w)
		n += n2
		if err != nil {
			return n, err
		}
	}
	n2, err := io.WriteString(w, "\r\n")
	n += n2
	return n, err
}

func (h Header) String() string {
	buf := new(bytes.Buffer)
	if _, err := h.Encode(buf); err != nil {
		return fmt.Sprintf("email.Header(encode error: %v)", err)
	}
	return buf.String()
}

// CanonicalKey builds a MIME header key out of bytes.
// It usually does this without allocating.
func CanonicalKey(keyBytes []byte) Key {
	b := make([]byte, 0, 64)
	b = append(keyBytes)
	asciiLower(b)

	// This list of standard headers was created by extracting
	// the headers and counting frequency out of an email archive.
	//
	// (for f in $(find . -type f); do \
	//	awk '/^\r$/{exit} {print $0}' $f | \   # extract the header section
	//	grep -v '^\s' | \                      # drop continued lines
	//	sed 's/\([A-Za-z0-9-]*\):.*/\1/'; \    # extract the header key
	// done) | sort | uniq -c | sort -nr
	switch string(b) {
	case "subject":
		return "Subject"
	case "date":
		return "Date"
	case "to":
		return "To"
	case "from":
		return "From"
	case "cc":
		return "CC"
	case "content-id":
		return "Content-ID"
	case "content-disposition":
		return "Content-Disposition"
	case "content-length":
		return "Content-Length"
	case "content-type":
		return "Content-Type"
	case "content-transfer-encoding":
		return "Content-Transfer-Encoding"
	case "received":
		return "Received"
	case "x-received":
		return "X-Received"
	case "return-path":
		return "Return-Path"
	case "arc-seal":
		return "ARC-Seal"
	case "arc-message-signature":
		return "ARC-Message-Signature"
	case "arc-authentication-results":
		return "ARC-Authentication-Results"
	case "received-spf":
		return "Received-SPF"
	case "delivered-to":
		return "Delivered-To"
	case "dkim-signature":
		return "DKIM-Signature"
	case "authentication-results":
		return "Authentication-Results"
	case "message-id":
		return "Message-ID"
	case "x-gm-message-state":
		return "X-Gm-Message-State"
	case "x-forwarded-to":
		return "X-Forwarded-To"
	case "x-forwarded-for":
		return "X-Forwarded-For"
	case "mime-version":
		return "MIME-Version"
	case "reply-to":
		return "Reply-To"
	case "feedback-id":
		return "Feedback-ID"
	case "references":
		return "References"
	case "list-id":
		return "List-ID"
	case "list-archive":
		return "List-Archive"
	case "list-help":
		return "List-Help"
	case "list-post":
		return "List-Post"
	case "list-unsubscribe":
		return "List-Unsubscribe"
	case "list-unsubscribe-post":
		return "List-Unsubscribe-Post"
	case "list-subscribe":
		return "List-Subscribe"
	case "list-owner":
		return "List-Owner"
	case "in-reply-to":
		return "In-Reply-To"
	case "x-ses-outgoing":
		return "X-SES-Outgoing"
	case "precedence":
		return "Precedence"
	case "x-original-messageid":
		return "X-Original-MessageID"
	case "x-mailer":
		return "X-Mailer"
	case "x-original-authentication-results":
		return "X-Original-Authentication-Results"
	case "x-auto-response-suppress":
		return "X-Auto-Response-Suppress"
	case "sender":
		return "Sender"
	case "x-report-abuse":
		return "X-Report-Abuse"
	case "x-csa-complaints":
		return "X-CSA-Complaints"
	case "x-campaign":
		return "X-Campaign"
	case "x-mc-user":
		return "X-MC-User"
	case "x-accounttype":
		return "X-Accounttype"
	case "x-originalarrivaltime":
		return "X-OriginalArrivalTime"
	case "domainkey-signature":
		return "DomainKey-Signature"
	case "x-beenthere":
		return "X-BeenThere"
	case "x-feedback-id":
		return "X-Feedback-ID"
	case "x-sent-to":
		return "X-Sent-To"
	case "x-binding":
		return "X-Binding"
	case "x-original-sender":
		return "X-Original-Sender"
	case "x-google-group-id":
		return "X-Google-Group-Id"
	case "x-google-dkim-signature":
		return "X-Google-DKIM-Signature"
	case "x-google-smtp-source":
		return "X-Google-Smtp-Source"
	case "x-google-id":
		return "X-Google-Id"
	case "x-emailtype-id":
		return "X-EmailType-Id"
	case "x-business-group":
		return "X-Business-Group"
	case "x-attach-flag":
		return "X-Attach-Flag"
	case "mailing-list":
		return "Mailing-list"
	case "errors-to":
		return "Errors-To"
	case "x-smtpapi":
		return "X-SMTPAPI"
	case "x-ms-tnef-correlator":
		return "X-MS-TNEF-Correlator"
	case "x-ms-has-attach":
		return "X-MS-Has-Attach"
	case "thread-topic":
		return "Thread-Topic"
	case "thread-index":
		return "Thread-Index"
	case "content-language":
		return "Content-Language"
	case "accept-language":
		return "Accept-Language"
	case "x-originatororg":
		return "X-OriginatorOrg"
	case "x-ms-exchange-transport-crosstenantheadersstamped":
		return "X-MS-Exchange-Transport-CrossTenantHeadersStamped"
	case "x-ms-exchange-crosstenant-network-message-id":
		return "X-MS-Exchange-CrossTenant-Network-Message-Id"
	case "x-antiabuse":
		return "X-AntiAbuse"
	case "x-mailer-name":
		return "X-Mailer-Name"
	case "x-ms-exchange-crosstenant-originalarrivaltime":
		return "X-MS-Exchange-CrossTenant-originalarrivaltime"
	case "x-ms-exchange-crosstenant-id":
		return "X-MS-Exchange-CrossTenant-id"
	case "x-ms-exchange-crosstenant-fromentityheader":
		return "X-MS-Exchange-CrossTenant-fromentityheader"
	case "x-proofpoint-virus-version":
		return "X-Proofpoint-Virus-Version"
	case "x-mktarchive":
		return "X-MktArchive"
	case "x-mailfrom":
		return "X-Mailfrom"
	case "x-msys-api":
		return "X-MSYS-API"
	case "x-priority":
		return "X-Priority"
	case "x-campaign-id":
		return "X-Campaign-ID"
	case "x-rptags":
		return "X-RPTags"
	case "x-proofpoint-spam-details":
		return "X-Proofpoint-Spam-Details"
	case "x-steam-message-type":
		return "X-Steam-Message-Type"
	case "x-notifications":
		return "X-Notifications"
	case "x-messageid":
		return "X-MessageID"
	case "x-listmember":
		return "X-ListMember"
	case "dkim-filter":
		return "DKIM-Filter"
	case "bounces-to":
		return "Bounces-To"
	case "x-virus-scanned":
		return "X-Virus-Scanned"
	case "x-roving-id":
		return "X-Roving-Id"
	case "x-roving-campaignid":
		return "X-Roving-Campaignid"
	case "x-return-path-hint":
		return "X-Return-Path-Hint"
	case "x-message-id":
		return "X-Message-ID"
	case "x-mailer_status":
		return "X-Mailer_Status"
	case "x-mailer_rsid":
		return "X-Mailer_RSID"
	case "x-mailer_profile":
		return "X-Mailer_Profile"
	case "x-mailer_osid":
		return "X-Mailer_OSID"
	case "x-channel-id":
		return "X-Channel-ID"
	case "x-campaign-activity-id":
		return "X-Campaign-Activity-ID"
	case "x-ctct-id":
		return "X-CTCT-ID"
	case "x-binding-id":
		return "X-Binding-ID"
	case "x-microsoft-exchange-diagnostics":
		return "X-Microsoft-Exchange-Diagnostics"
	case "x-mailman-version":
		return "X-Mailman-Version"
	case "x-mc-unique":
		return "X-MC-Unique"
	case "x-emarsys-identify":
		return "X-EMarSys-Identify"
	case "x-emarsys-environment":
		return "X-EMarSys-Environment"
	case "x-originating-ip":
		return "X-Originating-IP"
	case "x-spam-status":
		return "X-Spam-Status"
	case "x-spam-score":
		return "X-Spam-Score"
	case "x-spam-level":
		return "X-Spam-Level"
	case "x-spam-flag":
		return "X-Spam-Flag"
	case "x-source-sender":
		return "X-Source-Sender"
	case "x-source":
		return "X-Source"
	case "x-local-domain":
		return "X-Local-Domain"
	case "x-complaints-to":
		return "X-Complaints-To"
	case "x-broadcast-id":
		return "X-Broadcast-Id"
	case "x-bwhitelist":
		return "X-BWhitelist"
	case "x-account-notification-type":
		return "X-Account-Notification-Type"
	default:
		// Capitalize each letter following a '-'.
		for i, c := range b {
			if 'a' <= c && c <= 'z' {
				if i == 0 || (i > 0 && b[i-1] == '-') {
					b[i] -= 'a' - 'A'
				}
			}
		}
		return Key(b)
	}
}

func asciiLower(data []byte) {
	for i, b := range data {
		if b >= 'A' && b <= 'Z' {
			data[i] = b + ('a' - 'A')
		}
	}
}
