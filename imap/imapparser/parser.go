package imapparser

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"spilled.ink/imap/imapparser/utf7mod"
)

type Parser struct {
	Scanner *Scanner
	Mode    Mode

	Command Command
}

func (p *Parser) error(errctx string) error {
	if p.Scanner.Error != nil {
		return p.Scanner.Error
	}
	return parseErrorf(errctx)
}

func (p *Parser) parseMailbox(cmd *Command) (bool, error) {
	if !p.Scanner.Next(TokenString) {
		return false, nil
	}
	if len(p.Scanner.Value) == 5 && strings.EqualFold("INBOX", string(p.Scanner.Value)) {
		cmd.Mailbox = append(cmd.Mailbox, "INBOX"...)
	} else {
		var err error
		cmd.Mailbox, err = utf7mod.AppendDecode(cmd.Mailbox, p.Scanner.Value)
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

type TaggedError struct {
	Tag string
	Err error
}

func (te TaggedError) Error() string {
	errStr := "<nil>"
	if te.Err != nil {
		errStr = te.Err.Error()
	}
	return fmt.Sprintf("imapparser: %s %s", te.Tag, errStr)
}

type ParseError struct {
	msg string
}

func (e ParseError) Error() string { return e.msg }

func parseErrorf(format string, v ...interface{}) error {
	return ParseError{msg: fmt.Sprintf(format, v...)}
}

// ParseCommand parses an IMAP command.
//
// The result is filled into the Command field.
// Any []byte memory inside the Command (such as Tag) will be
// invalidated when the parser is invoked again.
//
// It will return an error if the command is for the wrong mode.
//
// If a command tag can be parsed before a parse error, the
// returned error will be a TaggedError.
func (p *Parser) ParseCommand() (err error) {
	defer func() {
		if err != nil {
			p.Scanner.Drain()
			if p.Scanner.Error != nil {
				// TODO: export ioErr?
				if p.Scanner.ioErr != nil {
					p.Command.reset()
					err = p.Scanner.ioErr
					return
				}
			}
			if len(p.Command.Tag) > 0 {
				err = TaggedError{
					Tag: string(p.Command.Tag),
					Err: err,
				}
			} else if _, isParseError := err.(ParseError); isParseError {
				// leave err as is
			} else {
				err = fmt.Errorf("imapparser: %v", err)
			}
			p.Command.reset()
		}
	}()
	if p.Command.Literal == nil {
		p.Command.Literal = p.Scanner.Literal
	}
	if p.Scanner.Literal == nil {
		p.Scanner.Literal = p.Command.Literal
	}
	p.Command.reset()
	cmd := &p.Command

	if !p.Scanner.Next(TokenTag) {
		return p.error("no command tag")
	}
	cmd.Tag = append(cmd.Tag, p.Scanner.Value...)

	if !p.Scanner.Next(TokenAtom) {
		return p.error("no command name")
	}
	asciiUpper(p.Scanner.Value)
	cmd.Name = commands[string(p.Scanner.Value)]
	if cmd.Name == "" {
		return fmt.Errorf("unknown command: %q", string(p.Scanner.Value))
	}

	if cmd.Name == "UID" {
		cmd.UID = true
		if !p.Scanner.Next(TokenAtom) {
			return p.error("no command name following UID prefix")
		}
		asciiUpper(p.Scanner.Value)
		cmd.Name = commands[string(p.Scanner.Value)]
		if cmd.Name == "" {
			return fmt.Errorf("unknown command: %q", string(p.Scanner.Value))
		}
		switch cmd.Name {
		case "COPY", "FETCH", "STORE", "SEARCH":
			// these commands support the UID prefix
		case "MOVE":
			// UID MOVE is part of RFC 6851
		case "EXPUNGE":
			// UID EXPUNGE is part of RFC 4315 UIDPLUS
		default:
			return fmt.Errorf("command %s does not support the UID prefix", cmd.Name)
		}
	}

	// Check command is valid in the current mode.
	var goodMode bool
	switch cmd.Name {
	case "CAPABILITY", "COMPRESS", "LOGOUT", "NOOP", "ID":
		goodMode = true // any mode is fine for these commands
	case "LOGIN", "AUTHENTICATE", "STARTTLS":
		goodMode = p.Mode == ModeNonAuth
	case "APPEND", "CREATE", "DELETE", "ENABLE", "EXAMINE", "IDLE", "LIST", "LSUB",
		"RENAME", "SELECT", "STATUS", "SUBSCRIBE", "UNSUBSCRIBE",
		"XAPPLEPUSHSERVICE":
		goodMode = p.Mode == ModeAuth || p.Mode == ModeSelected
	case "CHECK", "CLOSE", "EXPUNGE", "COPY", "MOVE", "FETCH", "STORE", "SEARCH":
		goodMode = p.Mode == ModeSelected
	}
	if !goodMode {
		return fmt.Errorf("bad mode for command %s", cmd.Name)
	}

	// Commands listed mostly in the order they appear in RFC 3501 section 6.
	switch cmd.Name {
	case "CAPABILITY", "NOOP", "LOGOUT", "STARTTLS":
		// no arguments

	case "COMPRESS": // RFC 4978
		if !p.Scanner.Next(TokenAtom) {
			return fmt.Errorf("COMPRESS missing mechanism name")
		}
		asciiUpper(p.Scanner.Value)
		if string(p.Scanner.Value) != "DEFLATE" {
			return fmt.Errorf("COMPRESS unsupported mechanism: %q", string(p.Scanner.Value))
		}

	case "ID":
		p.Scanner.Next(0)
		if p.Scanner.Token == TokenListStart {
		idLoop:
			for {
				p.Scanner.Next(0)
				switch p.Scanner.Token {
				case TokenListEnd:
					break idLoop
				case TokenString, TokenAtom:
					// TODO: TokenNIL? Remove it?
					if len(p.Scanner.Value) == 3 && string(p.Scanner.Value) == "NIL" {
						if len(cmd.Params)%2 == 1 {
							// Values can be NIL.
							cmd.Params = append(cmd.Params, nil)
						} else {
							return fmt.Errorf("ID NIL field name")
						}
					} else {
						cmd.Params = appendValue(cmd.Params, p.Scanner.Value)
					}
				default:
					return fmt.Errorf("ID unexpected parameter token %v", p.Scanner.Token)
				}
				if len(cmd.Params) > 100 {
					// RFC 2971 limits ID to 30 pairs, so this is generous.
					return fmt.Errorf("too many ID parameters")
				}
			}
		} else if p.Scanner.Token != TokenAtom || string(p.Scanner.Value) != "NIL" {
			return fmt.Errorf("ID missing parameter list, got %v", p.Scanner.Token)
		}
		if len(cmd.Params)%2 == 1 {
			return fmt.Errorf("ID parameter is missing value")
		}

	case "IDLE":
		if p.Scanner.ContFn != nil {
			p.Scanner.ContFn("+ idling\r\n", 0)
		}

	case "AUTHENTICATE":
		if !p.Scanner.Next(TokenString) {
			return fmt.Errorf("AUTHENTICATE missing mechanism")
		}
		if string(p.Scanner.Value) != "PLAIN" {
			return fmt.Errorf("AUTHENTICATE unsupported mechanism: %q", string(p.Scanner.Value))
		}
		if !p.Scanner.Next(TokenEnd) {
			return p.error("AUTHENTICATE has trailing argument")
		}
		if p.Scanner.ContFn != nil {
			p.Scanner.ContFn("+\r\n", 0)
		}

		// As documented in RFC 2595 Section 6. PLAIN SASL mechanism.
		//
		// Under PLAIN authentication the client sends a base64-encoded
		// string of the form:
		//
		//	authorize-id\0username\0password
		//
		// The authorize-id string is unused and may be empty.
		if !p.Scanner.Next(TokenString) {
			return fmt.Errorf("AUTHENTICATE credential is not a string")
		}
		dst := make([]byte, base64.StdEncoding.DecodedLen(len(p.Scanner.Value)))
		if n, err := base64.StdEncoding.Decode(dst, p.Scanner.Value); err != nil {
			return fmt.Errorf("AUTHENTICATE PLAIN invalid base64: %v", err)
		} else {
			dst = dst[:n]
		}
		if len(dst) < 4 {
			return fmt.Errorf("AUTHENTICATE PLAIN credentials too short")
		}
		i := bytes.IndexByte(dst, 0)
		if i == -1 {
			return fmt.Errorf("AUTHENTICATE PLAIN missing first dividing NUL")
		}
		// authorizeID = dst[:i]
		dst = dst[i+1:]
		i = bytes.IndexByte(dst, 0)
		if i == -1 {
			return fmt.Errorf("AUTHENTICATE PLAIN missing second dividing NUL")
		}
		if i == 0 {
			return fmt.Errorf("AUTHENTICATE PLAIN no username")
		}
		if i == len(dst)-1 {
			return fmt.Errorf("AUTHENTICATE PLAIN no password")
		}
		cmd.Auth.Username = append(cmd.Auth.Username, dst[:i]...)
		cmd.Auth.Password = append(cmd.Auth.Password, dst[i+1:]...)

	case "LOGIN":
		if !p.Scanner.Next(TokenString) {
			return fmt.Errorf("LOGIN missing username")
		}
		cmd.Auth.Username = append(cmd.Auth.Username, p.Scanner.Value...)
		if !p.Scanner.Next(TokenString) {
			return fmt.Errorf("LOGIN missing password")
		}
		cmd.Auth.Password = append(cmd.Auth.Password, p.Scanner.Value...)

	case "ENABLE": // RFC 5161
		for p.Scanner.NextOrEnd(TokenAtom) {
			if p.Scanner.Token == TokenEnd {
				if len(cmd.Params) == 0 {
					return fmt.Errorf("ENABLE missing required argument")
				}
				return nil
			}
			cmd.Params = appendValue(cmd.Params, p.Scanner.Value)
		}

	case "SELECT", "EXAMINE":
		return p.parseSelect(cmd)

	case "CREATE", "DELETE", "SUBSCRIBE", "UNSUBSCRIBE":
		if ok, err := p.parseMailbox(cmd); err != nil {
			return fmt.Errorf("%s bad mailbox name: %v", cmd.Name, err)
		} else if !ok {
			return fmt.Errorf("%s missing mailbox name", cmd.Name)
		}

	case "RENAME":
		if ok, err := p.parseMailbox(cmd); err != nil {
			return fmt.Errorf("RENAME bad existing mailbox name: %v", err)
		} else if !ok {
			return errors.New("RENAME missing existing mailbox name")
		}
		cmd.Rename.OldMailbox = append(cmd.Rename.OldMailbox, cmd.Mailbox...)
		cmd.Mailbox = cmd.Mailbox[:0]
		if ok, err := p.parseMailbox(cmd); err != nil {
			return fmt.Errorf("RENAME bad new mailbox name: %v", err)
		} else if !ok {
			return errors.New("RENAME missing new mailbox name")
		}
		cmd.Rename.NewMailbox = append(cmd.Rename.NewMailbox, cmd.Mailbox...)
		cmd.Mailbox = cmd.Mailbox[:0]

	case "LIST", "LSUB":
		if p.Scanner.Next(TokenListStart) {
			// RFC 5258 list-select-opts
			for {
				if p.Scanner.Next(TokenListEnd) {
					break
				}
				if !p.Scanner.Next(TokenString) {
					return errors.New("LIST bad selection option")
				}
				var opt string
				switch string(p.Scanner.Value) {
				case "SUBSCRIBED":
					opt = "SUBSCRIBED"
				case "REMOTE":
					opt = "REMOTE"
				case "RECURSIVEMATCH":
					opt = "RECURSIVEMATCH"
				case "SPECIAL-USE":
					opt = "SPECIAL-USE"
				default:
					return fmt.Errorf("LIST bad selection option")
				}
				cmd.List.SelectOptions = append(cmd.List.SelectOptions, opt)
			}
		}
		if !p.Scanner.Next(TokenString) {
			return fmt.Errorf("%s missing reference name", cmd.Name)
		}
		cmd.List.ReferenceName = append(cmd.List.ReferenceName, p.Scanner.Value...)
		if !p.Scanner.Next(TokenListMailbox) {
			return fmt.Errorf("%s missing mailbox glob", cmd.Name)
		}
		cmd.List.MailboxGlob = append(cmd.List.MailboxGlob, p.Scanner.Value...)

		if p.Scanner.NextOrEnd(TokenAtom) {
			if p.Scanner.Token == TokenEnd {
				return nil
			}
			if string(p.Scanner.Value) != "RETURN" {
				return errors.New("LIST expecting CRLF or RETURN options")
			}
			if !p.Scanner.Next(TokenListStart) {
				return errors.New("LIST RETURN options missing left-paren")
			}
			for {
				if p.Scanner.Next(TokenListEnd) {
					break
				}
				if !p.Scanner.Next(TokenString) {
					return errors.New("LIST RETURN invalid option")
				}
				var opt string
				switch string(p.Scanner.Value) {
				case "SUBSCRIBED":
					opt = "SUBSCRIBED"
				case "CHILDREN":
					opt = "CHILDREN"
				case "SPECIAL-USE":
					opt = "SPECIAL-USE"
				default:
					return fmt.Errorf("LIST bad RETURN option")
				}
				cmd.List.ReturnOptions = append(cmd.List.ReturnOptions, opt)
			}
		}

	case "STATUS":
		if ok, err := p.parseMailbox(cmd); err != nil {
			return fmt.Errorf("STATUS bad mailbox name: %v", err)
		} else if !ok {
			return errors.New("STATUS missing mailbox name")
		}

		if !p.Scanner.Next(TokenListStart) {
			return fmt.Errorf("STATUS missing list start")
		}
		for {
			if !p.Scanner.Next(TokenAtom) {
				break
			}
			var item StatusItem
			switch string(p.Scanner.Value) {
			case "MESSAGES":
				item = StatusMessages
			case "RECENT":
				item = StatusRecent
			case "UIDNEXT":
				item = StatusUIDNext
			case "UIDVALIDITY":
				item = StatusUIDValidity
			case "UNSEEN":
				item = StatusUnseen
			case "HIGHESTMODSEQ":
				item = StatusHighestModSeq
			default:
				return fmt.Errorf("STATUS unknown item: %s", p.Scanner.Value)
			}
			cmd.Status.Items = append(cmd.Status.Items, item)
		}
		if !p.Scanner.NextOrEnd(TokenListEnd) {
			return fmt.Errorf("STATUS missing list end")
		}

	case "APPEND":
		if ok, err := p.parseMailbox(cmd); err != nil {
			return fmt.Errorf("APPEND bad mailbox name: %v", err)
		} else if !ok {
			return errors.New("APPEND missing mailbox name")
		}

		p.Scanner.Next(0)

		// Optional flag-list.
		switch p.Scanner.Token {
		case TokenUnknown, TokenEnd:
			return fmt.Errorf("APPEND missing literal data")
		case TokenListStart:
			var err error
			for {
				if p.Scanner.NextOrEnd(TokenListEnd) {
					break
				}
				if !p.Scanner.Next(TokenFlag) {
					err = fmt.Errorf("APPEND expecting flag, got token %s", p.Scanner.Token)
					continue // until we find list end
				}
				cmd.Append.Flags = appendValue(cmd.Append.Flags, p.Scanner.Value)
			}
			if err != nil {
				return err
			}
			if p.Scanner.Token != TokenListEnd {
				return fmt.Errorf("APPEND missing flag list end")
			}
			p.Scanner.Next(0)
		}

		// Optional date-time.
		if p.Scanner.Token == TokenString {
			cmd.Append.Date = append(cmd.Append.Date, p.Scanner.Value...)
			p.Scanner.Next(TokenLiteral)
		}

		if p.Scanner.Token != TokenLiteral {
			return fmt.Errorf("APPEND missing literal data")
		}
		p.Scanner.Literal = nil

	case "CHECK", "CLOSE":
		// no arguments

	case "EXPUNGE":
		// EXPUNGE has no arguments
		// UID EXPUNGE takes a sequence set
		if cmd.UID {
			if !p.Scanner.Next(TokenSequences) {
				return fmt.Errorf("UID EXPUNGE missing sequences")
			}
			cmd.Sequences = append(cmd.Sequences, p.Scanner.Sequences...)
		}

	case "SEARCH":
		if err := p.parseSearchCommands(); err != nil {
			return err
		}
		return nil

	case "FETCH":
		if !p.Scanner.Next(TokenSequences) {
			return fmt.Errorf("FETCH missing sequences")
		}
		cmd.Sequences = append(cmd.Sequences, p.Scanner.Sequences...)

		if p.Scanner.Next(TokenListStart) {
			for {
				if !p.Scanner.Next(TokenFetchItem) {
					break
				}
				switch p.Scanner.FetchItem.Type {
				case FetchAll, FetchFull, FetchFast:
					// These types are only valid as top-level items.
					return fmt.Errorf("FETCH invalid item")
				}
				cmd.FetchItems = appendItem(cmd.FetchItems, &p.Scanner.FetchItem)
			}
			if p.Scanner.Error != nil {
				return err
			}
			if !p.Scanner.Next(TokenListEnd) {
				return fmt.Errorf("FETCH missing list end")
			}
			if len(cmd.FetchItems) == 0 {
				return fmt.Errorf("FETCH empty items list")
			}
		} else if p.Scanner.Next(TokenFetchItem) {
			cmd.FetchItems = appendItem(cmd.FetchItems, &p.Scanner.FetchItem)
		} else if p.Scanner.Error != nil {
			return p.Scanner.Error
		} else {
			return fmt.Errorf("FETCH missing items")
		}

		if cmd.UID {
			// UID FETCH implicitly includes UID. From RFC 3501:
			//
			// 	However, server implementations MUST implicitly
			//	include the UID message data item as part of
			//	any FETCH response caused by a UID command
			hasUID := false
			for _, item := range cmd.FetchItems {
				if item.Type == FetchUID {
					hasUID = true
				}
			}
			if !hasUID {
				cmd.FetchItems = append(cmd.FetchItems, FetchItem{
					Type: FetchUID,
				})
			}
		}

		p.Scanner.Next(0)

		// Optional FETCH modifiers.
		switch p.Scanner.Token {
		case TokenEnd:
			return nil // no modifiers
		case TokenListStart:
			// process modifiers below
		default:
			return fmt.Errorf("FETCH unexpected trailing token: %v", p.Scanner.Token)
		}

		for {
			if p.Scanner.NextOrEnd(TokenListEnd) {
				break
			}
			if !p.Scanner.Next(TokenAtom) {
				return fmt.Errorf("FETCH modifier expecting atom, got %s", p.Scanner.Token)
			}
			switch string(p.Scanner.Value) {
			case "CHANGEDSINCE":
				if !p.Scanner.Next(TokenNumber) {
					return fmt.Errorf("FETCH CHANGEDSINCE missing value")
				}
				// TODO: handle 0 value
				cmd.ChangedSince = int64(p.Scanner.Number)
			case "VANISHED":
				cmd.Vanished = true
			default:
				return fmt.Errorf("FETCH unknown modifier: %s", string(p.Scanner.Value))
			}
		}
		if p.Scanner.Token != TokenListEnd {
			return fmt.Errorf("FETCH modifiers missing list end")
		}

	case "STORE":
		if !p.Scanner.Next(TokenSequences) {
			return fmt.Errorf("STORE missing sequences")
		}
		cmd.Sequences = append(cmd.Sequences, p.Scanner.Sequences...)

		if p.Scanner.Next(TokenListStart) {
			if !p.Scanner.Next(TokenAtom) {
				return fmt.Errorf("STORE missing modifier name")
			}
			if string(p.Scanner.Value) != "UNCHANGEDSINCE" {
				return fmt.Errorf("STORE unknown modifier: %s", string(p.Scanner.Value))
			}
			if !p.Scanner.Next(TokenNumber) {
				return fmt.Errorf("STORE UNCHANGEDSINCE missing value")
			}
			cmd.Store.UnchangedSince = int64(p.Scanner.Number)
			if !p.Scanner.Next(TokenListEnd) {
				return fmt.Errorf("STORE missing list end")
			}
		} else if p.Scanner.Error != nil {
			return p.Scanner.Error
		}

		if !p.Scanner.Next(TokenAtom) {
			return fmt.Errorf("STORE missing data item name")
		}
		switch string(p.Scanner.Value) {
		case "+FLAGS":
			cmd.Store.Mode = StoreAdd
		case "+FLAGS.SILENT":
			cmd.Store.Mode = StoreAdd
			cmd.Store.Silent = true
		case "-FLAGS":
			cmd.Store.Mode = StoreRemove
		case "-FLAGS.SILENT":
			cmd.Store.Mode = StoreRemove
			cmd.Store.Silent = true
		case "FLAGS":
			cmd.Store.Mode = StoreReplace
		case "FLAGS.SILENT":
			cmd.Store.Mode = StoreReplace
			cmd.Store.Silent = true
		default:
			return fmt.Errorf("STORE invalid name: %q", string(p.Scanner.Value))
		}

		if !p.Scanner.Next(TokenListStart) {
			return fmt.Errorf("STORE missing flag list")
		}
		for {
			if !p.Scanner.Next(TokenFlag) {
				break
			}
			cmd.Store.Flags = appendValue(cmd.Store.Flags, p.Scanner.Value)
		}
		if !p.Scanner.Next(TokenListEnd) {
			return fmt.Errorf("STORE missing flag list end")
		}

	case "COPY", "MOVE":
		if !p.Scanner.Next(TokenSequences) {
			return fmt.Errorf("COPY missing sequences")
		}
		cmd.Sequences = append(cmd.Sequences, p.Scanner.Sequences...)

		if ok, err := p.parseMailbox(cmd); err != nil {
			return fmt.Errorf("%s mailbox name: %v", cmd.Name, err)
		} else if !ok {
			return fmt.Errorf("%smissing mailbox name", cmd.Name)
		}

	case "XAPPLEPUSHSERVICE":
		aps := &ApplePushService{}
		cmd.ApplePushService = aps
		for {
			if !p.Scanner.NextOrEnd(TokenString) {
				return fmt.Errorf("unexpected %s", p.Scanner.Token)
			}
			if p.Scanner.Token == TokenEnd {
				return nil
			}

			switch string(p.Scanner.Value) {
			case "mailboxes":
				if !p.Scanner.Next(TokenListStart) {
					return fmt.Errorf("XAPPLEPUSHSERVICE mailboxes missing list")
				}
				for {
					if p.Scanner.Next(TokenListEnd) {
						break
					}
					if !p.Scanner.Next(TokenString) {
						return fmt.Errorf("XAPPLEPUSHSERVICE mailbox is not a string")
					}
					aps.Mailboxes = append(aps.Mailboxes, string(p.Scanner.Value))
				}
			case "aps-version":
				if !p.Scanner.Next(TokenNumber) {
					return fmt.Errorf("XAPPLEPUSHSERVICE invalid aps-version")
				}
				aps.Version = int(p.Scanner.Number)
			case "aps-account-id":
				if !p.Scanner.Next(TokenString) {
					return fmt.Errorf("XAPPLEPUSHSERVICE invalid aps-account-id")
				}
				aps.Device.AccountID = string(p.Scanner.Value)
			case "aps-device-token":
				if !p.Scanner.Next(TokenString) {
					return fmt.Errorf("XAPPLEPUSHSERVICE invalid aps-device-token")
				}
				aps.Device.DeviceToken = string(p.Scanner.Value)
			case "aps-subtopic":
				if !p.Scanner.Next(TokenString) {
					return fmt.Errorf("XAPPLEPUSHSERVICE invalid aps-subtopic")
				}
				aps.Subtopic = string(p.Scanner.Value)
			default:
				return fmt.Errorf("XAPPLEPUSHSERVICE unknown parameter %q", string(p.Scanner.Value))
			}
		}

	default:
		return fmt.Errorf("unsupported command: %v", cmd.Name)
	}

	if !p.Scanner.Next(TokenEnd) {
		return p.error(cmd.Name + " has trailing arguments")
	}
	return nil
}

var commands = map[string]string{
	"CAPABILITY":        "CAPABILITY",
	"COMPRESS":          "COMPRESS",
	"LOGOUT":            "LOGOUT",
	"NOOP":              "NOOP",
	"LOGIN":             "LOGIN",
	"AUTHENTICATE":      "AUTHENTICATE",
	"STARTTLS":          "STARTTLS",
	"APPEND":            "APPEND",
	"CREATE":            "CREATE",
	"DELETE":            "DELETE",
	"ENABLE":            "ENABLE",
	"ID":                "ID",
	"IDLE":              "IDLE",
	"EXAMINE":           "EXAMINE",
	"LIST":              "LIST",
	"LSUB":              "LSUB",
	"RENAME":            "RENAME",
	"SELECT":            "SELECT",
	"STATUS":            "STATUS",
	"SUBSCRIBE":         "SUBSCRIBE",
	"UNSUBSCRIBE":       "UNSUBSCRIBE",
	"CHECK":             "CHECK",
	"CLOSE":             "CLOSE",
	"EXPUNGE":           "EXPUNGE",
	"COPY":              "COPY",
	"MOVE":              "MOVE",
	"FETCH":             "FETCH",
	"STORE":             "STORE",
	"SEARCH":            "SEARCH",
	"UID":               "UID",
	"XAPPLEPUSHSERVICE": "XAPPLEPUSHSERVICE",
}

var searchKeys = map[string]SearchKey{
	"AND":    SearchKey("AND"),
	"SEQSET": SearchKey("SEQSET"),

	"ALL":        SearchKey("ALL"),
	"ANSWERED":   SearchKey("ANSWERED"),
	"BCC":        SearchKey("BCC"),
	"BEFORE":     SearchKey("BEFORE"),
	"BODY":       SearchKey("BODY"),
	"CC":         SearchKey("CC"),
	"DELETED":    SearchKey("DELETED"),
	"DRAFT":      SearchKey("DRAFT"),
	"FLAGGED":    SearchKey("FLAGGED"),
	"FROM":       SearchKey("FROM"),
	"HEADER":     SearchKey("HEADER"),
	"KEYWORD":    SearchKey("KEYWORD"),
	"LARGER":     SearchKey("LARGER"),
	"NEW":        SearchKey("NEW"),
	"NOT":        SearchKey("NOT"),
	"OLD":        SearchKey("OLD"),
	"ON":         SearchKey("ON"),
	"OR":         SearchKey("OR"),
	"RECENT":     SearchKey("RECENT"),
	"SEEN":       SearchKey("SEEN"),
	"SENTBEFORE": SearchKey("SENTBEFORE"),
	"SENTON":     SearchKey("SENTON"),
	"SENTSINCE":  SearchKey("SENTSINCE"),
	"SINCE":      SearchKey("SINCE"),
	"SMALLER":    SearchKey("SMALLER"),
	"SUBJECT":    SearchKey("SUBJECT"),
	"TEXT":       SearchKey("TEXT"),
	"TO":         SearchKey("TO"),
	"UID":        SearchKey("UID"),
	"UNANSWERED": SearchKey("UNANSWERED"),
	"UNDELETED":  SearchKey("UNDELETED"),
	"UNDRAFT":    SearchKey("UNDRAFT"),
	"UNFLAGGED":  SearchKey("UNFLAGGED"),
	"UNKEYWORD":  SearchKey("UNKEYWORD"),
	"UNSEEN":     SearchKey("UNSEEN"),
	"MODSEQ":     SearchKey("MODSEQ"),
}

func (p *Parser) parseSelect(cmd *Command) error {
	if ok, err := p.parseMailbox(cmd); err != nil {
		return fmt.Errorf("%s bad mailbox name: %v", cmd.Name, err)
	} else if !ok {
		return fmt.Errorf("%s missing mailbox name", cmd.Name)
	}
	if !p.Scanner.NextOrEnd(TokenListStart) {
		return fmt.Errorf("%s trailing token: %s", cmd.Name, p.Scanner.Token)
	}
	if p.Scanner.Token == TokenEnd {
		return nil
	}
	for {
		if p.Scanner.Next(TokenListEnd) {
			break
		}
		if !p.Scanner.Next(TokenAtom) {
			return fmt.Errorf("%s missing paramter name", cmd.Name)
		}
		switch string(p.Scanner.Value) {
		case "CONDSTORE":
			cmd.Condstore = true
		case "QRESYNC": // RFC 7162 Section 3.2.5.
			if !p.Scanner.Next(TokenListStart) {
				return fmt.Errorf("%s missing QRESYNC parameter list", cmd.Name)
			}
			if !p.Scanner.Next(TokenNumber) {
				return fmt.Errorf("%s QRESYNC UIDVALIDITY missing", cmd.Name)
			}
			if p.Scanner.Number > math.MaxUint32 {
				return fmt.Errorf("%s QRESYNC UIDVALIDITY too big", cmd.Name)
			}
			if p.Scanner.Number == 0 {
				return fmt.Errorf("%s QRESYNC UIDVALIDITY invalid", cmd.Name)
			}
			cmd.Qresync.UIDValidity = uint32(p.Scanner.Number)
			if !p.Scanner.Next(TokenNumber) {
				return fmt.Errorf("%s missing QRESYNC MODSEQ", cmd.Name)
			}
			cmd.Qresync.ModSeq = int64(p.Scanner.Number)
			if p.Scanner.Next(TokenListEnd) {
				return nil // next two parameters are optional
			}

			//  known-uids
			if !p.Scanner.Next(TokenSequences) {
				return fmt.Errorf("%s bad QRESYNC known UIDs sequence", cmd.Name)
			}
			cmd.Qresync.UIDs = append(cmd.Qresync.UIDs, p.Scanner.Sequences...)
			if len(cmd.Qresync.UIDs) == 1 && (cmd.Qresync.UIDs[0] == SeqRange{}) {
				return fmt.Errorf("%s bad QRESYNC known UIDs sequence, '*' is not allowed", cmd.Name)
			}
			if p.Scanner.Next(TokenListEnd) {
				return nil // parameter is optional
			}

			// seq-match-data
			if !p.Scanner.Next(TokenListStart) {
				return fmt.Errorf("%s bad QRESYNC match list start", cmd.Name)
			}
			if !p.Scanner.Next(TokenSequences) {
				return fmt.Errorf("%s bad QRESYNC match list UIDs sequence", cmd.Name)
			}
			cmd.Qresync.KnownSeqNumMatch = append(cmd.Qresync.KnownSeqNumMatch, p.Scanner.Sequences...)
			if len(cmd.Qresync.KnownSeqNumMatch) == 1 && (cmd.Qresync.KnownSeqNumMatch[0] == SeqRange{}) {
				return fmt.Errorf("%s bad QRESYNC known seqnum match, '*' is not allowed", cmd.Name)
			}
			if !p.Scanner.Next(TokenSequences) {
				return fmt.Errorf("%s bad QRESYNC match list UIDs sequence", cmd.Name)
			}
			cmd.Qresync.KnownUIDMatch = append(cmd.Qresync.KnownUIDMatch, p.Scanner.Sequences...)
			if len(cmd.Qresync.KnownUIDMatch) == 1 && (cmd.Qresync.KnownUIDMatch[0] == SeqRange{}) {
				return fmt.Errorf("%s bad QRESYNC known UIDs match, '*' is not allowed", cmd.Name)
			}
			if !p.Scanner.Next(TokenListEnd) {
				return fmt.Errorf("%s missing QRESYNC match list end", cmd.Name)
			}
			if !p.Scanner.Next(TokenListEnd) {
				return fmt.Errorf("%s missing QRESYNC parameter list end", cmd.Name)
			}
		default:
			return fmt.Errorf("%s invalid paramter: %s", cmd.Name, p.Scanner.Value)
		}
	}
	if !p.Scanner.Next(TokenEnd) {
		return p.error(cmd.Name + " has trailing arguments")
	}
	return nil
}

func (p *Parser) parseSearchCommands() error {
	if !p.Scanner.Next(TokenSearchKey) {
		return p.error("missing search key")
	}
	asciiUpper(p.Scanner.Value)
	if string(p.Scanner.Value) == "CHARSET" {
		if !p.Scanner.Next(TokenString) {
			return p.error("missing CHARSET value")
		}
		asciiUpper(p.Scanner.Value)
		switch string(p.Scanner.Value) {
		case "UTF-8":
			p.Command.Search.Charset = "UTF-8"
		case "US-ASCII":
			p.Command.Search.Charset = "US-ASCII"
		default:
			return p.error("unsupported CHARSET")
		}

		if !p.Scanner.Next(TokenSearchKey) {
			return p.error("missing search key")
		}
		asciiUpper(p.Scanner.Value)
	}
	if string(p.Scanner.Value) == "RETURN" {
		// ESEARCH RFC 4731. Grammar defined in RFC 4466.
		if !p.Scanner.Next(TokenListStart) {
			return p.error("missing search RETURN list")
		}
	returnLoop:
		for {
			if !p.Scanner.Next(TokenSearchKey) {
				break
			}
			asciiUpper(p.Scanner.Value)
			var val string
			switch string(p.Scanner.Value) {
			case "MIN":
				val = "MIN"
			case "MAX":
				val = "MAX"
			case "ALL":
				val = "ALL"
			case "COUNT":
				val = "COUNT"
			case ")": // TODO: should this scan as a TokenSearchKey?
				break returnLoop
			default:
				return fmt.Errorf("unknown search RETURN value: %q", string(p.Scanner.Value))
			}
			p.Command.Search.Return = append(p.Command.Search.Return, val)
		}

		if len(p.Command.Search.Return) == 0 {
			// RFC4 731 says RETURN () is equivalent to ALL.
			p.Command.Search.Return = append(p.Command.Search.Return, "ALL")
		}

		if !p.Scanner.Next(TokenSearchKey) {
			return p.error("missing search key")
		}
		asciiUpper(p.Scanner.Value)
	}

	rootOp := &SearchOp{
		Key: "AND",
	}
	p.Command.Search.Op = rootOp

	for {
		op, err := p.parseSearchKey()
		if err != nil {
			p.Command.Search.Op = nil
			return err
		}
		rootOp.Children = append(rootOp.Children, *op)

		if !p.Scanner.NextOrEnd(TokenSearchKey) {
			break
		}
		asciiUpper(p.Scanner.Value)
		if p.Scanner.Token == TokenEnd {
			break
		}
	}

	if len(rootOp.Children) == 1 {
		p.Command.Search.Op = &rootOp.Children[0]
	}

	return p.Scanner.Error
}

// parseSearchKey parses a search-key.
// It requires Scanner.Next(TokenSearchKey) already be successfully called.
func (p *Parser) parseSearchKey() (*SearchOp, error) {
	op := &SearchOp{}
	if len(p.Scanner.Sequences) > 0 {
		op.Key = "SEQSET"
		op.Sequences = append(([]SeqRange)(nil), p.Scanner.Sequences...)
		return op, nil
	}

	op.Key = searchKeys[string(p.Scanner.Value)]
	if op.Key == "" {
		if len(p.Scanner.Value) == 1 && p.Scanner.Value[0] == '(' {
			op.Key = "AND"
		} else {
			return nil, fmt.Errorf("SEARCH key unknown: %q", string(p.Scanner.Value))
		}
	}

	switch op.Key {
	case "ALL", "ANSWERED", "DELETED", "FLAGGED", "NEW", "OLD", "RECENT", "SEEN",
		"UNANSWERED", "UNDELETED", "UNFLAGGED", "UNSEEN", "DRAFT":
		return op, nil
	case "BCC", "BODY", "CC", "FROM", "SUBJECT", "TEXT", "TO":
		if !p.Scanner.Next(TokenString) {
			return nil, p.error(fmt.Sprintf("search key %s missing string argument", op.Key))
		}
		op.Value = string(p.Scanner.Value)
		return op, nil
	case "KEYWORD", "UNKEYWORD":
		if !p.Scanner.Next(TokenAtom) { // flag-keyword
			return nil, fmt.Errorf("SEARCH key %s missing atom argument", op.Key)
		}
		op.Value = string(p.Scanner.Value)
		return op, nil
	case "BEFORE", "ON", "SINCE", "SENTBEFORE", "SENTON", "SENTSINCE":
		if !p.Scanner.Next(TokenDate) {
			return nil, fmt.Errorf("SEARCH %s missing date", op.Key)
		}
		op.Date = p.Scanner.Date
		return op, nil
	case "HEADER":
		if !p.Scanner.Next(TokenString) { // header-fld-name
			return nil, fmt.Errorf("SEARCH HEADER missing field name")
		}
		b := make([]byte, 0, 128)
		b = append(b, p.Scanner.Value...)
		b = append(b, ':', ' ')
		if !p.Scanner.Next(TokenString) {
			return nil, fmt.Errorf("SEARCH HEADER missing field value")
		}
		b = append(b, p.Scanner.Value...)
		op.Value = string(b)
		return op, nil

	case "LARGER", "SMALLER":
		if !p.Scanner.Next(TokenNumber) {
			return nil, fmt.Errorf("SEARCH %s invalid number", op.Key)
		}
		op.Num = int64(p.Scanner.Number)
		return op, nil

	case "NOT":
		// search-key
		if !p.Scanner.Next(TokenSearchKey) {
			return nil, fmt.Errorf("SEARCH key NOT missing term")
		}
		asciiUpper(p.Scanner.Value)
		ch, err := p.parseSearchKey()
		if err != nil {
			return nil, err
		}
		op.Children = append(op.Children, *ch)
		return op, nil

	case "OR":
		// search-key SP search-key
		if !p.Scanner.Next(TokenSearchKey) {
			return nil, fmt.Errorf("SEARCH key OR missing first term")
		}
		asciiUpper(p.Scanner.Value)
		ch, err := p.parseSearchKey()
		if err != nil {
			return nil, err
		}
		op.Children = append(op.Children, *ch)

		if !p.Scanner.Next(TokenSearchKey) {
			return nil, fmt.Errorf("SEARCH key OR missing second term")
		}
		asciiUpper(p.Scanner.Value)
		ch, err = p.parseSearchKey()
		if err != nil {
			return nil, err
		}
		op.Children = append(op.Children, *ch)
		return op, nil

	case "UID", "UNDRAFT":
		// sequence-set
		if !p.Scanner.Next(TokenSequences) {
			return nil, fmt.Errorf("SEARCH key %s missing sequence-set", op.Key)
		}
		op.Sequences = append(([]SeqRange(nil)), p.Scanner.Sequences...)
		return op, nil

	case "AND":
		// search-key *(SP search-key) ")"
		for {
			if !p.Scanner.Next(TokenSearchKey) {
				return nil, fmt.Errorf("SEARCH key list missing closing ')'")
			}
			asciiUpper(p.Scanner.Value)
			if string(p.Scanner.Value) == ")" {
				break
			}

			ch, err := p.parseSearchKey()
			if err != nil {
				return nil, err
			}
			op.Children = append(op.Children, *ch)
		}
		if len(op.Children) == 0 {
			return nil, fmt.Errorf("SEARCH empty key list")
		}
		if len(op.Children) == 1 {
			return &op.Children[0], nil
		}

		return op, nil

	case "MODSEQ": // RFC 7162 Section 3.1.5, part of CONDSTORE
		p.Scanner.Next(0)
		if p.Scanner.Token == TokenString { // entry-name: "/flags/<V>"
			if !p.Scanner.Next(TokenAtom) { // entry-type-req
				return nil, fmt.Errorf("SEARCH MODSEQ missing entry-type")
			}
			// We throw these values away as we are parsing for
			// a server that does per-message mod-sequences, and
			// RFC 7162 tells us to:
			//
			//	If the server doesn't store separate mod-sequences
			//	for different metadata items, it MUST ignore
			//	<entry-name> and <entry-type-req>.
			p.Scanner.Next(0)
		}
		if p.Scanner.Token != TokenAtom {
			return nil, fmt.Errorf("SEARCH MODSEQ invalid number, got %s", p.Scanner.Token)
		}
		v, err := strconv.ParseInt(string(p.Scanner.Value), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("SEARCH MODSEQ invalid number: %v", err)
		}
		op.Num = v
		return op, nil
	}

	return nil, errors.New("SEARCH parse todo")
}
