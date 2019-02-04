package msgbuilder

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"unicode/utf8"

	"spilled.ink/email"
)

type TreeNode struct {
	Header PartHeader
	Part   *email.Part // nil for multipart containers
	Kids   []TreeNode
}

type PartHeader struct {
	ContentType             string // includes params like "; charset=..."
	ContentID               string // includes <...> quoting
	ContentDisposition      string // includes params like "; filename=...""
	ContentTransferEncoding string
}

func (hdr PartHeader) ForEach(fn func(key email.Key, val string)) {
	fn("Content-Disposition", hdr.ContentDisposition)
	fn("Content-ID", hdr.ContentID)
	if hdr.ContentTransferEncoding == "7bit" {
		fn("Content-Transfer-Encoding", "")
	} else {
		fn("Content-Transfer-Encoding", hdr.ContentTransferEncoding)
	}
	fn("Content-Type", hdr.ContentType)
}

func BuildTree(msg *email.Msg) (*TreeNode, error) {
	rnd := rand.New(rand.NewSource(msg.Seed))

	body, related, attachments, err := pullParts(msg)
	if err != nil {
		return nil, fmt.Errorf("msgbuilder.BuildTree: %s: %v", msg.MsgID, err)
	}

	bodyNode, err := buildTreeBody(rnd, body, related)
	if err != nil {
		return nil, fmt.Errorf("msgbuilder.BuildTree: %s: %v", msg.MsgID, err)
	}

	if len(attachments) == 0 {
		return &bodyNode, nil
	}

	boundary := randBoundary(rnd)
	root := &TreeNode{
		Header: PartHeader{
			ContentType: "multipart/mixed; boundary=" + quoteSpecial(boundary),
		},
		Kids: []TreeNode{bodyNode},
	}
	for _, a := range attachments {
		hdr, err := buildPartHeader(a)
		if err != nil {
			return nil, fmt.Errorf("msgbuilder.BuildTree: %s: %v", msg.MsgID, err)
		}
		root.Kids = append(root.Kids, TreeNode{
			Header: hdr,
			Part:   a,
		})
	}

	// TODO: fill out part.Path

	return root, nil
}

func buildTreeBody(rnd *rand.Rand, body, related []*email.Part) (TreeNode, error) {
	if len(body) == 0 {
		return TreeNode{}, errors.New("no body")
	}

	if len(body) == 1 {
		return buildTreeRelated(rnd, body[0], related)
	}

	boundary := randBoundary(rnd)
	node := TreeNode{
		Header: PartHeader{
			ContentType: "multipart/alternative; boundary=" + quoteSpecial(boundary),
		},
	}
	seenHTML := false
	for _, b := range body {
		var rel []*email.Part
		if b.ContentType == "text/html" && !seenHTML {
			seenHTML = true
			rel = related
		}
		bNode, err := buildTreeRelated(rnd, b, rel)
		if err != nil {
			return TreeNode{}, err
		}
		node.Kids = append(node.Kids, bNode)
	}
	return node, nil
}

func buildTreeRelated(rnd *rand.Rand, body *email.Part, related []*email.Part) (TreeNode, error) {
	bodyHdr, err := buildPartHeader(body)
	if err != nil {
		return TreeNode{}, err
	}
	node := TreeNode{
		Header: bodyHdr,
		Part:   body,
	}
	if len(related) == 0 {
		return node, nil
	}

	boundary := randBoundary(rnd)
	node = TreeNode{
		Header: PartHeader{
			ContentType: "multipart/related; boundary=" + quoteSpecial(boundary),
		},
		Kids: []TreeNode{node},
	}
	for _, r := range related {
		rNode, err := buildTreeRelated(rnd, r, nil)
		if err != nil {
			return TreeNode{}, err
		}
		node.Kids = append(node.Kids, rNode)
	}
	return node, nil
}

func pullParts(msg *email.Msg) (body, related, attachments []*email.Part, err error) {
	for i := 0; i < len(msg.Parts); i++ {
		p := &msg.Parts[i]
		if p.IsBody {
			body = append(body, p)
			continue
		}
		if p.Name == "" {
			p.Name = "attachment-" + strconv.Itoa(i)
		}
		if p.ContentID == "" {
			attachments = append(attachments, p)
		} else {
			related = append(related, p)
		}
	}
	return body, related, attachments, nil
}

func quoteSpecial(v string) string {
	// RFC 2045 mentions that special characters must be
	// quoted in parameter values.
	quoted := false
loop:
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '(', ')', '<', '>', '@',
			',', ';', ':', '\\', '"',
			'/', '[', ']', '?', '=':
			quoted = true
			break loop
		}
	}
	if quoted {
		return strconv.Quote(v)
	}
	return v
}

func extractMediaType(v string) (mediatype string) {
	i := strings.Index(v, ";")
	if i == -1 {
		return v
	}
	return strings.TrimSpace(strings.ToLower(v[0:i]))
}

func buildPartHeader(part *email.Part) (hdr PartHeader, err error) {
	hdr.ContentType = part.ContentType
	if hdr.ContentType == "text/plain" || hdr.ContentType == "text/html" {
		hdr.ContentType += `; charset="UTF-8"`
	}

	if part.ContentID != "" {
		if strings.Contains(part.ContentID, `"`) {
			// TODO: encode any '"' character in the name
			return PartHeader{}, fmt.Errorf("part %d header: Content-ID %q includes quotes", part.PartNum, part.ContentID)
		}

		hdr.ContentID = "<" + part.ContentID + ">"
		fileName := part.Name
		if fileName == "" {
			fileName = part.ContentID
		}
		hdr.ContentDisposition = `inline; filename="` + fileName + `"`
	} else if part.Name != "" {
		name := part.Name
		if strings.Contains(name, `"`) {
			// TODO: encode any '"' character in the name
			return PartHeader{}, fmt.Errorf("part %d header: attachment name %q includes quotes", part.PartNum, name)
		}
		if hdr.ContentType != "" {
			hdr.ContentType += `; name="` + name + `"`
		}
		hdr.ContentDisposition = `attachment; filename="` + name + `"`
	} else {
		hdr.ContentDisposition = "inline"
	}

	// Determine Content-Transfer-Encoding.
	if part.ContentTransferEncoding != "" {
		// Skip this if part.ContentTransferEncoding is already set.
		hdr.ContentTransferEncoding = part.ContentTransferEncoding
		return hdr, nil
	}
	if part.Content == nil {
		return PartHeader{}, fmt.Errorf("part %d header: no content", part.PartNum)
	}
	if _, err := part.Content.Seek(0, 0); err != nil {
		return PartHeader{}, fmt.Errorf("part %d header: %v", part.PartNum, err)
	}
	isASCII := true // all ASCII, no NULs
	is7Bit := true  // isASCII and lines are short
	r := bufio.NewReader(part.Content)
bufloop:
	for {
		line, isPrefix, err := r.ReadLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			return PartHeader{}, fmt.Errorf("part %d header: %v", part.PartNum, err)
		}
		if isPrefix || len(line) > 120 {
			is7Bit = false
		}
		for _, c := range line {
			if c == 0 || c >= utf8.RuneSelf {
				isASCII = false
				is7Bit = false
				break bufloop
			}
		}
	}
	if _, err := part.Content.Seek(0, 0); err != nil {
		return PartHeader{}, fmt.Errorf("part %d header: %v", part.PartNum, err)
	}
	if t := extractMediaType(hdr.ContentType); isASCII || t == "text/plain" || t == "text/html" {
		if is7Bit {
			// No Content-Transfer-Encoding
			// TODO: convert \n to \r\n?
			hdr.ContentTransferEncoding = "7bit"
		} else {
			hdr.ContentTransferEncoding = "quoted-printable"
		}
	} else {
		hdr.ContentTransferEncoding = "base64"
	}

	return hdr, nil
}

func (node TreeNode) String() string {
	buf := new(bytes.Buffer)
	node.debugPrint(buf, 0)
	return buf.String()
}

func debugIndent(buf *bytes.Buffer, indent int) {
	for i := 0; i < indent; i++ {
		buf.WriteByte('\t')
	}
}

func (node *TreeNode) debugPrint(buf *bytes.Buffer, indent int) {
	buf.WriteString("TreeNode{\n")
	debugIndent(buf, indent+1)
	buf.WriteString("Header: {")
	wroteHeader := false
	node.Header.ForEach(func(key email.Key, val string) {
		if val == "" {
			return
		}
		wroteHeader = true
		buf.WriteByte('\n')
		debugIndent(buf, indent+2)
		fmt.Fprintf(buf, "%s: %q", key, val)
	})
	if wroteHeader {
		buf.WriteByte('\n')
		debugIndent(buf, indent+1)
	}
	buf.WriteString("}\n")

	if node.Part != nil {
		debugIndent(buf, indent+1)
		fmt.Fprintf(buf, "Part: %v\n", node.Part)
	}

	if len(node.Kids) > 0 {
		debugIndent(buf, indent+1)
		buf.WriteString("Kids: {\n")
		for i := range node.Kids {
			kid := &node.Kids[i]
			debugIndent(buf, indent+2)
			fmt.Fprintf(buf, "%d: ", i)
			kid.debugPrint(buf, indent+2)
		}
		debugIndent(buf, indent+1)
		buf.WriteString("}\n")
	}

	debugIndent(buf, indent)
	buf.WriteString("}\n")
}
