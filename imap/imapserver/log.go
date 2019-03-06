package imapserver

import (
	"fmt"
	"strings"
	"time"

	"spilled.ink/email"
)

type logMsg struct {
	What     string
	When     time.Time
	Duration time.Duration
	ID       string
	UserID   int64
	MsgID    email.MsgID
	PartNum  int
	Err      error
	Data     string
}

func (l logMsg) String() string {
	const where = "imap"

	buf := new(strings.Builder)
	fmt.Fprintf(buf, `{"where": %q, "what": %q, `, where, l.What)

	if l.When.IsZero() {
		l.When = time.Now()
	}
	buf.WriteString(`"when": "`)
	buf.Write(l.When.AppendFormat(make([]byte, 0, 64), time.RFC3339Nano))
	buf.WriteString(`"`)

	if l.Duration != 0 {
		fmt.Fprintf(buf, `, "duration": "%s"`, l.Duration)
	}
	if l.ID != "" {
		fmt.Fprintf(buf, `, "session_id": "%s"`, l.ID)
	}
	if l.UserID != 0 {
		fmt.Fprintf(buf, `, "user_id": "%d"`, l.UserID)
	}
	if l.MsgID != 0 {
		fmt.Fprintf(buf, `, "msg_id": "%d"`, l.MsgID)
	}
	if l.PartNum != 0 {
		fmt.Fprintf(buf, `, "part_num": "%d"`, l.PartNum)
	}
	if l.Err != nil {
		fmt.Fprintf(buf, `, "err": %q`, l.Err.Error())
	}
	if l.Data != "" {
		fmt.Fprintf(buf, `, "data": "%s"`, l.Data)
	}
	buf.WriteByte('}')
	return buf.String()
}
