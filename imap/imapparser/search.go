package imapparser

import (
	"strings"
	"time"
)

type MatchMessage interface {
	SeqNum() uint32
	UID() uint32
	ModSeq() int64
	Flag(name string) bool
	Header(name string) string
	Date() time.Time
	RFC822Size() int64
}

type Matcher struct {
	op *SearchOp
}

func NewMatcher(op *SearchOp) (*Matcher, error) {
	// TODO: check keys are valid
	return &Matcher{op: op}, nil
}

func (m *Matcher) Match(msg MatchMessage) bool {
	return m.match(msg, m.op)
}

func (m *Matcher) match(msg MatchMessage, op *SearchOp) bool {
	switch op.Key {
	case "AND":
		for _, op := range op.Children {
			if !m.match(msg, &op) {
				return false
			}
		}
		return true
	case "OR":
		for _, op := range op.Children {
			if m.match(msg, &op) {
				return true
			}
		}
		return false
	case "SEQSET":
		return SeqContains(op.Sequences, msg.SeqNum())
	case "UID":
		return SeqContains(op.Sequences, msg.UID())
	case "ALL":
		return true
	case "BEFORE":
		return msg.Date().Before(op.Date)
	case "KEYWORD":
		// TODO
	case "LARGER":
		return msg.RFC822Size() > op.Num
	case "SMALLER":
		return msg.RFC822Size() < op.Num
	case "MODSEQ":
		return msg.ModSeq() >= op.Num
	case "NEW":
		// equivalent to (RECENT UNSEEN)
		return msg.Flag(`\Recent`) && !msg.Flag(`\Seen`)
	case "NOT":
		if len(op.Children) != 1 {
			return false // bad AST, avoid panic
		}
		return !m.match(msg, &op.Children[0])
	case "OLD":
		return !msg.Flag(`\Recent`)
	case "ON":
		// Ignore time.
		year, month, day := msg.Date().Date()
		return time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Equal(op.Date)
	case "RECENT":
		return msg.Flag(`\Recent`)
	case "SEEN":
		return msg.Flag(`\Seen`)
	case "SENTBEFORE":
		// TODO
	case "SENTON":
		// TODO
	case "SENTSINCE":
		// TODO
	case "SINCE":
		year, month, day := msg.Date().Date()
		t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		return t.Equal(op.Date) || t.After(op.Date)
	case "HEADER":
		i := strings.IndexByte(op.Value, ':')
		if i < 1 {
			return false
		}
		name := op.Value[:i]
		value := ""
		if i < len(op.Value)-1 {
			value = op.Value[i+2:]
		}
		return msg.Header(name) == value
	case "SUBJECT":
		return strings.Contains(msg.Header("Subject"), op.Value)
	case "TO":
		return strings.Contains(msg.Header("To"), op.Value)
	case "FROM":
		return strings.Contains(msg.Header("From"), op.Value)
	case "CC":
		return strings.Contains(msg.Header("CC"), op.Value)
	case "BCC":
		return strings.Contains(msg.Header("BCC"), op.Value)
	case "BODY":
		// TODO
	case "TEXT":
		// TODO
	case "ANSWERED":
		return msg.Flag(`\Answered`)
	case "UNANSWERED":
		return !msg.Flag(`\Answered`)
	case "DELETED":
		return msg.Flag(`\Deleted`)
	case "UNDELETED":
		return !msg.Flag(`\Deleted`)
	case "DRAFT":
		return msg.Flag(`\Draft`)
	case "UNDRAFT":
		return !msg.Flag(`\Draft`)
	case "FLAGGED":
		return msg.Flag(`\Flagged`)
	case "UNFLAGGED":
		return !msg.Flag(`\Flagged`)
	case "UNKEYWORD":
		// TODO
	case "UNSEEN":
		return !msg.Flag(`\Seen`)
	}
	return false
}

func SeqContains(sequences []SeqRange, seqNum uint32) bool {
	for _, seq := range sequences {
		if seq.Min <= seqNum && (seq.Max == 0 || seq.Max >= seqNum) {
			return true
		}
	}
	return false
}
