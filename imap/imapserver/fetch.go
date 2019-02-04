package imapserver

import (
	"fmt"
	"io"
	"mime"
	"sort"
	"strings"
	"net/mail"

	"spilled.ink/email"
	"spilled.ink/email/msgbuilder"
	"spilled.ink/imap"
	"spilled.ink/imap/imapparser"
)

func (c *Conn) cmdFetch() {
	cmd := &c.p.Command

	for i := range cmd.FetchItems {
		if cmd.FetchItems[i].Type == imapparser.FetchModSeq {
			c.setCondStore()
			break
		}
	}

	// Sort any BODY requests to the back of the fetch items.
	// Typical BODY fetches are large literals, while other
	// items are small.
	//
	// Some clients (like macOS Mail) make requests like
	//	(BODY.PEEK[] BODYSTRUCTURE)
	// and other IMAP servers reorder these items.
	items := cmd.FetchItems[:0]
	bodyParts := make([]imapparser.FetchItem, 0, 4)
	for _, item := range cmd.FetchItems {
		if item.Type == imapparser.FetchBody {
			bodyParts = append(bodyParts, item)
		} else {
			items = append(items, item)
		}
	}
	for _, item := range bodyParts {
		items = append(items, item)
	}

	fn := func(m imap.Message) {
		c.writef("* %d FETCH (", m.Summary().SeqNum)
		for i := range cmd.FetchItems {
			item := &cmd.FetchItems[i]
			if i > 0 {
				c.writef(" ")
			}
			c.writeItem(m, item)
		}
		c.writef(")\r\n")
	}
	changedSince := cmd.ChangedSince
	if changedSince == 0 {
		changedSince = -1
	}
	err := c.mailbox.Fetch(cmd.UID, cmd.Sequences, changedSince, fn)
	if err != nil {
		c.respondln("BAD FETCH error: %v", err)
		return
	}
	if cmd.UID {
		c.respondln("OK UID FETCH completed")
	} else {
		c.respondln("OK FETCH completed")
	}
}

func fetchItemType(t imapparser.FetchItemType) *imapparser.FetchItem {
	return &imapparser.FetchItem{Type: t}
}

func (c *Conn) writeItem(m imap.Message, item *imapparser.FetchItem) {
	switch item.Type {
	case imapparser.FetchAll:
		c.writeItem(m, fetchItemType(imapparser.FetchFlags))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchInternalDate))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchRFC822Size))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchEnvelope))
	case imapparser.FetchFull:
		c.writeItem(m, fetchItemType(imapparser.FetchFlags))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchInternalDate))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchRFC822Size))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchEnvelope))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchBody))
	case imapparser.FetchFast:
		c.writeItem(m, fetchItemType(imapparser.FetchFlags))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchInternalDate))
		c.writef(" ")
		c.writeItem(m, fetchItemType(imapparser.FetchRFC822Size))
	case imapparser.FetchEnvelope:
		hdrs := m.Msg().Headers
		c.writef("ENVELOPE (")
		c.writeStringBytes(hdrs.Get("Date"))
		c.writef(" ")
		c.writeStringBytes(hdrs.Get("Subject"))
		c.writef(" ")
		c.writeAddresses(hdrs.Get("From"))
		c.writef(" ")
		c.writeAddresses(hdrs.Get("Sender"))
		c.writef(" ")
		c.writeAddresses(hdrs.Get("Reply-To"))
		c.writef(" ")
		c.writeAddresses(hdrs.Get("To"))
		c.writef(" ")
		c.writeAddresses(hdrs.Get("CC"))
		c.writef(" ")
		c.writeAddresses(hdrs.Get("BCC"))
		c.writef(" ")
		c.writeStringBytes(hdrs.Get("In-Reply-To"))
		c.writef(" ")
		c.writeStringBytes(hdrs.Get("Message-ID"))
		c.writef(")")
	case imapparser.FetchFlags:
		c.writef("FLAGS (")
		for i, flag := range m.Msg().Flags {
			if i > 0 {
				c.writef(" ")
			}
			if flag[0] == '\\' {
				c.writef("%s", flag)
			} else {
				c.writeString(flag)
			}
		}
		c.writef(")")
	case imapparser.FetchInternalDate:
		c.writef("INTERNALDATE ")
		c.writeString(m.Msg().Date.Format("02-Jan-2006 15:04:05 -0700"))
	case imapparser.FetchRFC822Header:
		c.writeBody(m, &imapparser.FetchItem{
			Type: imapparser.FetchBody,
			Section: imapparser.FetchItemSection{
				Name: "HEADER",
			},
		})
	case imapparser.FetchRFC822Size:
		c.writef("RFC822.SIZE %d", m.Msg().EncodedSize)
	case imapparser.FetchRFC822Text:
		c.writeBody(m, &imapparser.FetchItem{
			Type: imapparser.FetchBody,
			Section: imapparser.FetchItemSection{
				Name: "TEXT",
			},
		})
	case imapparser.FetchUID:
		c.writef("UID %d", m.Summary().UID)
	case imapparser.FetchModSeq:
		c.writef("MODSEQ (%d)", m.Summary().ModSeq)
	case imapparser.FetchBodyStructure:
		c.writeBodyStructure(m)
	case imapparser.FetchBody:
		c.writeBody(m, item)
	default:
		panic(fmt.Sprintf("imapserver: impossible fetch item: %v", item))
	}
}

func (c *Conn) writeAddresses(addrBytes []byte) {
	addrs, err := mail.ParseAddressList(string(addrBytes))
	if err != nil {
		c.writef("NIL")
		c.Logf("cannot write addresses %q: %v", addrBytes, err)
		return
	}
	for _, addr := range addrs {
		i := strings.LastIndexByte(addr.Address, '@')
		if i == -1 {
			c.Logf("cannot write address: %q", addr.Address)
			continue
		}
		mailboxName, hostName := addr.Address[:i], addr.Address[i+1:]

		c.writef("(")
		if addr.Name == "" {
			c.writef("NIL")
		} else {
			c.writeString(addr.Name) // personal name
		}
		c.writef(" NIL ") // at-domain-list (source route)
		c.writeString(mailboxName)
		c.writef(" ")
		c.writeString(hostName)
		c.writef(")")
	}
}

func (c *Conn) writeBodyStructure(m imap.Message) {
	node, err := msgbuilder.BuildTree(m.Msg())
	if err != nil {
		c.Logf("BODYSTRUCTURE: %v", err)
		return
	}
	c.writef("BODYSTRUCTURE (")
	c.writeBodyStructurePart(node)
	c.writef(")")
}

func (c *Conn) writeBodyStructurePart(node *msgbuilder.TreeNode) {
	partNum := -1
	if node.Part != nil {
		partNum = node.Part.PartNum
	}
	mediaType, ctParams, err := mime.ParseMediaType(node.Header.ContentType)
	if err != nil {
		c.Logf("BODYSTRUCTURE part %d: %v", partNum, err)
		return
	}
	var ctParamKeys []string
	for key := range ctParams {
		ctParamKeys = append(ctParamKeys, key)
	}
	sort.Strings(ctParamKeys)
	var bodyType, bodySubtype string
	if i := strings.IndexByte(mediaType, '/'); i == -1 {
		c.Logf("BODYSTRUCTURE part %d bad mediatype: %s", partNum, mediaType)
		return
	} else {
		bodyType, bodySubtype = mediaType[:i], mediaType[i+1:]
	}

	if len(node.Kids) > 0 {
		// multipart
		for i, kid := range node.Kids {
			if i > 0 {
				c.writef(" (")
			} else {
				c.writef("(")
			}
			c.writeBodyStructurePart(&kid)
			c.writef(")")
		}

		// subtype
		c.writef(" ")
		c.writeString(strings.ToUpper(bodySubtype))
		// body parameter parenthesized list
		c.writef(" (boundary ")
		c.writeString(ctParams["boundary"]) // TODO: all ctParamKeys?
		c.writef(")")
		// body disposition
		if node.Header.ContentDisposition == "" {
			c.writef(" NIL")
		} else {
			c.writef(" ()") // TODO
		}
		// body language
		c.writef(" NIL")
		// body location
		c.writef(" NIL")
		return
	}

	// body type
	c.writeString(bodyType)
	c.writef(" ")
	// body subtype
	c.writeString(bodySubtype)
	// body parameter parnthesized list
	c.writef(" (")
	for i, key := range ctParamKeys {
		if i > 0 {
			c.writef(" ")
		}
		c.writeString(key)
		c.writef(" ")
		c.writeString(ctParams[key])
	}
	c.writef(")")
	// body id
	if node.Header.ContentID == "" {
		c.writef(" NIL")
	} else {
		c.writef(" ")
		c.writeString(node.Header.ContentID)
	}
	// body description
	c.writef(" NIL")
	// body encoding
	c.writef(" ")
	if node.Header.ContentTransferEncoding == "7bit" {
		c.writef("NIL")
	} else {
		c.writeString(node.Header.ContentTransferEncoding)
	}
	c.writef(" %d", node.Part.ContentTransferSize) // body size
	if bodyType == "text" {
		// RFC 3501 7.4.2:
		//	A body type of type TEXT contains, immediately after
		//	the basic fields, the size of the body in text lines.
		c.writef(" %d", node.Part.ContentTransferLines)
	}
}

func (c *Conn) loadParts(m imap.Message, node *msgbuilder.TreeNode) error {
	if node.Part != nil && node.Part.Content == nil {
		if err := m.LoadPart(node.Part.PartNum); err != nil {
			return err
		}
	}
	for i := range node.Kids {
		if err := c.loadParts(m, &node.Kids[i]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Conn) writeBody(m imap.Message, item *imapparser.FetchItem) {
	// item.Type == imapparser.FetchBody
	// BODY[<section>]<<origin octet>>

	buf := c.server.Filer.BufferFile(0)
	defer buf.Close()

	node, err := msgbuilder.BuildTree(m.Msg())
	if err != nil {
		c.Logf("BODY %v: %v", m.Msg().MsgID, err)
		return
	}
	if len(item.Section.Path) > 0 {
		// BODY[1.2.3]
		node = findPath(node, item.Section.Path)
		if node == nil {
			c.Logf("BODY %v: cannot find path %v", m.Msg().MsgID, item.Section.Path)
			return
		}
	}

	switch item.Section.Name {
	case "":
		if len(item.Section.Path) > 0 {
			// BODY[1.2.3]
			if node.Part == nil {
				c.Logf("BODY %v: path %v has no part", m.Msg().MsgID, item.Section.Path)
				return
			}
			if err := m.LoadPart(node.Part.PartNum); err != nil {
				c.Logf("BODY %v: %d: ", node.Part.PartNum, err)
				return
			}
			if err := msgbuilder.EncodeContent(buf, node.Header, node.Part); err != nil {
				c.Logf("BODY %v: encode: %v", node.Part.PartNum, err)
				return
			}
		} else {
			// BODY[]
			if err := c.loadParts(m, node); err != nil {
				c.Logf("BODY[] %v", err)
				return
			}
			builder := &msgbuilder.Builder{Filer: c.server.Filer}
			var err error
			if err = builder.Build(buf, m.Msg()); err != nil {
				c.Logf("BODY[]: %v", err)
				return
			}
		}

	case "HEADER", "MIME":
		var hdr email.Header
		if len(item.Section.Path) > 0 {
			node.Header.ForEach(func(key email.Key, val string) {
				if val != "" {
					hdr.Add(key, []byte(val))
				}
			})
		} else {
			hdr = m.Msg().Headers
		}
		if _, err := hdr.Encode(buf); err != nil {
			c.Logf("HEADER: %v", err)
			return
		}
	case "HEADER.FIELDS.NOT":
		if len(item.Section.Path) > 0 {
			// TODO: use node.Header
			c.Logf("HEADER.FIELDS.NOT TODO part")
			return
		}

		not := make(map[email.Key]bool)
		for _, name := range item.Section.Headers {
			key := email.CanonicalKey(name)
			not[key] = true
		}

		var hdr email.Header
		for _, entry := range m.Msg().Headers.Entries {
			if not[entry.Key] {
				continue
			}
			hdr.Add(entry.Key, entry.Value)
		}
		if _, err := hdr.Encode(buf); err != nil {
			c.Logf("HEADER.FIELDS.NOT: %v", err)
			return
		}
	case "HEADER.FIELDS":
		if len(item.Section.Path) > 0 {
			// TODO: use node.Header
			c.Logf("HEADER.FIELDS TODO part")
			return
		}

		hdrs := m.Msg().Headers
		var hdr email.Header
		for _, name := range item.Section.Headers {
			key := email.CanonicalKey(name)
			if v := hdrs.Get(key); len(v) != 0 {
				hdr.Add(key, v)
			}
		}
		if _, err := hdr.Encode(buf); err != nil {
			c.Logf("HEADER.FIELDS: %v", err)
			return
		}
	case "TEXT":
		// like BODY[] but without any headers
		if err := c.loadParts(m, node); err != nil {
			c.Logf("TEXT: %v", err)
			return
		}
		builder := &msgbuilder.Builder{Filer: c.server.Filer}
		if err := builder.WriteNode(buf, node); err != nil {
			c.Logf("TEXT: %v", err)
			return
		}
	default:
		c.Logf("FETCH BODY %v unknown section: %q", m.Msg().MsgID, item.Section.Name)
		return
	}

	if !item.Peek {
		seen := false
		for _, flag := range m.Msg().Flags {
			if flag == `\Seen` {
				seen = true
			}
		}
		if !seen {
			if err := m.SetSeen(); err != nil {
				c.Logf("FETCH BODY failed to set Seen flag on %s", m.Msg().MsgID)
			}
		}
	}

	if _, err := buf.Seek(0, 0); err != nil {
		c.Logf("BODY: buf seek: %v", err)
		return
	}

	c.writef("BODY[")
	for i, v := range item.Section.Path {
		if i > 0 {
			c.writef(".")
		}
		c.writef("%d", v)
	}
	if item.Section.Name != "" {
		if len(item.Section.Path) > 0 {
			c.writef(".")
		}
		c.writef(item.Section.Name)
	}
	switch item.Section.Name {
	case "HEADER.FIELDS", "HEADER.FIELDS.NOT":
		c.writef(" (")
		for i, name := range item.Section.Headers {
			if i > 0 {
				c.writef(" ")
			}
			c.writeString(string(email.CanonicalKey(name)))
		}
		c.writef(")")
	}
	c.writef("]")

	r := io.Reader(buf)
	size := buf.Size()
	if item.Partial.Start != 0 || item.Partial.Length != 0 {
		start := int64(item.Partial.Start)
		if start > size {
			start = size
		}
		l := int64(item.Partial.Length)
		if l > size-start {
			l = size - start
		}

		buf.Seek(start, 0)
		size = l
		r = io.LimitReader(buf, size)
		c.writef("<%d> ", start)
	} else {
		c.writef(" ")
	}
	c.writeLiteral(r, size)
}

func findPath(node *msgbuilder.TreeNode, path []uint16) *msgbuilder.TreeNode {
	if len(path) == 1 && path[0] == 1 && len(node.Kids) == 0 {
		return node
	}
	for len(path) > 0 {
		if int(path[0])-1 >= len(node.Kids) {
			return nil
		}
		node = &node.Kids[path[0]-1]
		path = path[1:]
	}
	return node
}
