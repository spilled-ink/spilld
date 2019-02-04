package imapparser

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
)

func FormatSeqs(w io.Writer, seqs []SeqRange) error {
	for i, seq := range seqs {
		if i > 0 {
			if _, err := fmt.Fprint(w, ","); err != nil {
				return err
			}
		}
		if seq.Min == 0 && seq.Max == 0 {
			if _, err := fmt.Fprint(w, "*"); err != nil {
				return err
			}
			continue
		}
		if seq.Min == seq.Max {
			if _, err := fmt.Fprintf(w, "%d", seq.Min); err != nil {
				return err
			}
			continue
		}
		if seq.Min == 0 {
			if _, err := fmt.Fprint(w, "*"); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "%d", seq.Min); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprint(w, ":"); err != nil {
			return err
		}
		if seq.Max == 0 {
			if _, err := fmt.Fprint(w, "*"); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "%d", seq.Max); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s Store) String() string {
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s", s.Mode)
	if s.Silent {
		buf.WriteString(".SILENT")
	}
	if s.UnchangedSince != 0 {
		fmt.Fprintf(buf, " (UNCHANGEDSINCE %d)", s.UnchangedSince)
	}
	if len(s.Flags) > 0 {
		buf.WriteString("(")
		for i, f := range s.Flags {
			if i > 0 {
				buf.WriteByte(' ')
			}
			buf.Write(f)
		}
		buf.WriteByte(')')
	}
	return buf.String()
}

func (c Command) String() string {
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "Command{Tag: %q, Name: %q, ", string(c.Tag), string(c.Name))
	if c.UID {
		fmt.Fprint(buf, "UID, ")
	}
	if len(c.Mailbox) > 0 {
		fmt.Fprintf(buf, "Mailbox: %q, ", string(c.Mailbox))
	}
	if c.Condstore {
		fmt.Fprintf(buf, "Condstore, ")
	}
	if c.Qresync.ModSeq != 0 || c.Qresync.UIDValidity != 0 || len(c.Qresync.UIDs) > 0 {
		fmt.Fprintf(buf, "Qresync: {%d %d %v {%v %v}}, ", c.Qresync.UIDValidity, c.Qresync.ModSeq, c.Qresync.UIDs, c.Qresync.KnownSeqNumMatch, c.Qresync.KnownUIDMatch)
	}
	if len(c.Sequences) > 0 {
		fmt.Fprintf(buf, "Sequences: %v, ", c.Sequences)
	}
	if len(c.Rename.OldMailbox) > 0 || len(c.Rename.NewMailbox) > 0 {
		fmt.Fprintf(buf, "Rename: {%q, %q}, ", c.Rename.OldMailbox, c.Rename.NewMailbox)
	}
	if len(c.Params) > 0 {
		fmt.Fprintf(buf, "Params: %q, ", string(bytes.Join(c.Params, []byte(", "))))
	}
	if len(c.Auth.Username) > 0 || len(c.Auth.Password) > 0 {
		fmt.Fprintf(buf, "Auth: {%q, %q}, ", c.Auth.Username, c.Auth.Password)
	}
	if len(c.List.MailboxGlob) > 0 || len(c.List.ReferenceName) > 0 {
		fmt.Fprintf(buf, "List: {%v, %q, %q, %v}, ", c.List.SelectOptions, c.List.MailboxGlob, c.List.ReferenceName, c.List.ReturnOptions)
	}
	if len(c.Status.Items) > 0 {
		fmt.Fprintf(buf, "Status: {%v}, ", c.Status.Items)
	}
	if len(c.Append.Flags) > 0 || len(c.Append.Date) > 0 {
		flags := string(bytes.Join(c.Append.Flags, []byte(", ")))
		fmt.Fprintf(buf, "Append: {%q, %q}, ", flags, c.Append.Date)
	}
	if len(c.FetchItems) > 0 {
		fmt.Fprintf(buf, "Fetch: {")
		for i, item := range c.FetchItems {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteString(item.String())
		}
		buf.WriteString("}, ")
	}
	if c.ChangedSince != 0 {
		fmt.Fprintf(buf, "ChangedSince: %d, ", c.ChangedSince)
	}
	if c.Vanished {
		fmt.Fprintf(buf, "Vanished, ")
	}
	if c.Store.Mode != 0 {
		fmt.Fprintf(buf, "Store: {%s}, ", c.Store.String())
	}
	if c.Search.Op != nil {
		fmt.Fprintf(buf, "Search: {%v %q %v}, ", c.Search.Op, string(c.Search.Charset), c.Search.Return)
	}
	if c.ApplePushService != nil {
		fmt.Fprintf(buf, "ApplePushService: %+v, ", c.ApplePushService)
	}

	if c.Literal != nil && c.Literal.Size() > 0 {
		r := io.NewSectionReader(c.Literal, 0, c.Literal.Size())
		b, err := ioutil.ReadAll(r)
		if err != nil {
			fmt.Fprintf(buf, "Literal: err=%v, ", err)
		} else {
			fmt.Fprintf(buf, "Literal: %q, ", string(b))
		}
	}

	return strings.TrimSuffix(buf.String(), ", ") + "}"
}

func clearBytes(b *[]byte) {
	if *b != nil {
		*b = (*b)[:0]
	}
}

func (cmd *Command) reset() {
	clearBytes(&cmd.Tag)
	cmd.Name = ""
	cmd.UID = false
	clearBytes(&cmd.Mailbox)
	cmd.Condstore = false
	cmd.Qresync.UIDValidity = 0
	cmd.Qresync.ModSeq = 0
	cmd.Qresync.UIDs = nil             // rarely used
	cmd.Qresync.KnownSeqNumMatch = nil // rarely used
	cmd.Qresync.KnownUIDMatch = nil    // rarely used
	if cmd.Sequences != nil {
		cmd.Sequences = cmd.Sequences[:0]
	}
	if cmd.Literal != nil {
		if err := cmd.Literal.Truncate(0); err != nil {
			panic(err)
		}
		if _, err := cmd.Literal.Seek(0, 0); err != nil {
			panic(err)
		}
	}
	clearBytes(&cmd.Rename.OldMailbox)
	clearBytes(&cmd.Rename.NewMailbox)
	cmd.Params = nil // rarely used (ENABLE, ID), so release the memory
	clearBytes(&cmd.Auth.Username)
	clearBytes(&cmd.Auth.Password)
	cmd.List.SelectOptions = cmd.List.SelectOptions[:0]
	cmd.List.ReturnOptions = cmd.List.ReturnOptions[:0]
	clearBytes(&cmd.List.ReferenceName)
	clearBytes(&cmd.List.MailboxGlob)
	if cmd.Status.Items != nil {
		cmd.Status.Items = cmd.Status.Items[:0]
	}
	cmd.Append.Flags = clearValues(cmd.Append.Flags)
	clearBytes(&cmd.Append.Date)
	cmd.FetchItems = clearItems(cmd.FetchItems)
	cmd.ChangedSince = 0
	cmd.Vanished = false
	cmd.Store.Mode = 0
	cmd.Store.Silent = false
	cmd.Store.UnchangedSince = 0
	cmd.Store.Flags = clearValues(cmd.Store.Flags)
	cmd.Search.Op = nil
	cmd.Search.Charset = ""
	cmd.Search.Return = cmd.Search.Return[:0]
	cmd.ApplePushService = nil // rarely used, release memory
}

func clearItems(items []FetchItem) []FetchItem {
	if items == nil {
		return nil
	}
	items = items[:cap(items)]
	for i := range items {
		items[i].reset()
	}
	return items[:0]
}

func clearValues(values [][]byte) [][]byte {
	if values == nil {
		return nil
	}
	values = values[:cap(values)]
	for i := range values {
		values[i] = values[i][:0]
	}
	return values[:0]
}

func appendValue(values [][]byte, src []byte) [][]byte {
	if len(values) < cap(values) {
		values = values[:len(values)+1]
	} else {
		values = append(values, make([]byte, 0, len(src)))
	}
	values[len(values)-1] = append(values[len(values)-1], src...)
	return values
}

func appendItem(items []FetchItem, src *FetchItem) []FetchItem {
	if len(items) < cap(items) {
		items = items[:len(items)+1]
	} else {
		items = append(items, FetchItem{})
	}
	copyItem(&items[len(items)-1], src)
	return items
}

func AppendSeqRange(seqs []SeqRange, v uint32) []SeqRange {
	if len(seqs) > 0 && v > 0 {
		last := &seqs[len(seqs)-1]
		if last.Min > last.Max {
			last.Min, last.Max = last.Max, last.Min // normalize
		}
		if last.Max > 0 && last.Max == v-1 {
			last.Max++ // append v to last SeqRange
			return seqs
		}
	}
	return append(seqs, SeqRange{Min: v, Max: v})
}

func (item *FetchItem) reset() {
	item.Type = ""
	item.Peek = false
	item.Section.Name = ""
	if item.Section.Path != nil {
		item.Section.Path = item.Section.Path[:0]
	}
	item.Section.Headers = clearValues(item.Section.Headers)
	item.Partial.Start = 0
	item.Partial.Length = 0
}

func copyItem(dst, src *FetchItem) {
	dst.Type = src.Type
	dst.Peek = src.Peek
	dst.Section.Name = src.Section.Name
	dst.Section.Path = append(dst.Section.Path[:0], src.Section.Path...)
	dst.Section.Headers = dst.Section.Headers[:0]
	for _, h := range src.Section.Headers {
		dst.Section.Headers = appendValue(dst.Section.Headers, h)
	}
	dst.Partial.Start = src.Partial.Start
	dst.Partial.Length = src.Partial.Length
}

func (item *FetchItem) String() string {
	if item == nil {
		return "FetchItem(nil)"
	}
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%s", item.Type)
	if item.Peek {
		fmt.Fprint(buf, ".PEEK")
	}
	s := item.Section
	if len(s.Path) != 0 || s.Name != "" || len(s.Headers) != 0 {
		buf.WriteByte('[')
		for i, v := range s.Path {
			if i > 0 {
				buf.WriteByte('.')
			}
			fmt.Fprintf(buf, "%d", v)
		}
		if s.Name != "" {
			if len(s.Path) > 0 {
				buf.WriteByte('.')
			}
			buf.WriteString(s.Name)
		}
		if len(s.Headers) > 0 {
			buf.WriteString(" (")
			for i, h := range s.Headers {
				if i > 0 {
					buf.WriteByte(' ')
				}
				buf.Write(h)
			}
			buf.WriteByte(')')
		}
		buf.WriteByte(']')
	}
	if item.Partial.Start != 0 || item.Partial.Length != 0 {
		fmt.Fprintf(buf, "<%d.%d>", item.Partial.Start, item.Partial.Length)
	}
	return buf.String()
}

func (s StoreMode) String() string {
	switch s {
	case StoreUnknown:
		return "StoreUnknown"
	case StoreAdd:
		return "+FLAGS"
	case StoreRemove:
		return "-FLAGS"
	case StoreReplace:
		return "FLAGS"
	default:
		return fmt.Sprintf("StoreMode(%d)", int(s))
	}
}
