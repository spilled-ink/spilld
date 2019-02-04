package imaptest

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"spilled.ink/imap/imapserver"
)

func TestNonAuth(t *testing.T, server *TestServer) {
	s := server.OpenSession(t)
	defer s.Shutdown()
	if line := s.read(); !strings.HasPrefix(line, "* OK") {
		t.Fatalf("bad initial ok: %q", line)
	}
	s.write("t01 NOOP\r\n")
	if got, want := s.read(), "t01 OK"; !strings.HasPrefix(got, want) {
		t.Fatalf("NOOP resposne: %q, want prefix %q", got, want)
	}
	// TODO: CAPABILITY
	// TODO: LOGOUT
}

func TestLogin(t *testing.T, server *TestServer) {
	s := server.OpenSession(t)
	defer s.Shutdown()
	s.read() // initial * OK
	s.login()
}

// TODO: TestAUTHENTICATE

func TestList(t *testing.T, server *TestServer) {
	s := server.OpenSession(t)
	defer s.Shutdown()
	s.read() // initial * OK
	s.login()

	s.write(`01 LIST "" ""` + "\r\n")
	s.readExpectPrefix(`* LIST (\Noselect) "/" ""`)
	s.readExpectPrefix(`01 OK`)

	s.write(`01 LIST "" "*"` + "\r\n")
	s.readExpectPrefix(`* LIST (\HasNoChildren) "/" INBOX`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Archive) "/" Archive`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Drafts) "/" Drafts`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Sent) "/" Sent`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Junk) "/" Spam`)
	s.readExpectPrefix(`* LIST (\HasNoChildren) "/" Subscriptions`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Flagged) "/" TestFlagged`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Trash) "/" Trash`)
	s.readExpectPrefix(`01 OK`)

	s.write(`01 LIST "" "*" RETURN (SPECIAL-USE)` + "\r\n")
	s.readExpectPrefix(`* LIST (\HasNoChildren) "/" INBOX`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Archive) "/" Archive`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Drafts) "/" Drafts`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Sent) "/" Sent`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Junk) "/" Spam`)
	s.readExpectPrefix(`* LIST (\HasNoChildren) "/" Subscriptions`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Flagged) "/" TestFlagged`)
	s.readExpectPrefix(`* LIST (\HasNoChildren \Trash) "/" Trash`)
	s.readExpectPrefix(`01 OK`)

}

func TestSelect(t *testing.T, server *TestServer) {
	s := server.OpenSession(t)
	defer s.Shutdown()
	s.read() // initial * OK
	s.login()

	s.write("01 SELECT INBOX\r\n")
	s.readExpectPrefix(`* 4 EXISTS`)
	s.readExpectPrefix(`* 0 RECENT`)
	s.readExpectPrefix(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen`)
	s.readExpectPrefix(`* OK [PERMANENTFLAGS (`)
	s.readExpectPrefix(`* OK [HIGHESTMODSEQ`)
	s.readExpectPrefix(`* OK [UNSEEN 1]`)
	s.readExpectPrefix(`* OK [UIDVALIDITY`)
	s.readExpectPrefix(`* OK [UIDNEXT 6]`)
	s.readExpectPrefix(`01 OK [READ-WRITE]`)
}

func TestStatus(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	s.write("01 STATUS INBOX (MESSAGES RECENT UIDNEXT UNSEEN UIDVALIDITY)\r\n")
	s.readExpectPrefix(`* STATUS INBOX (MESSAGES 4 RECENT 0 UIDNEXT 6 UNSEEN 4 UIDVALIDITY`)
	s.readExpectPrefix(`01 OK`)
}

func TestSearch(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	s.write("02 UID SEARCH 2:* NOT DELETED\r\n")
	s.readExpectPrefix(`* SEARCH 3 4 5`)
	s.readExpectPrefix(`02 OK`)

	s.write("03 SEARCH 2:* NOT DELETED\r\n")
	s.readExpectPrefix(`* SEARCH 2 3 4`)
	s.readExpectPrefix(`03 OK`)

	s.write("04 SEARCH 1:* HEADER Message-ID \"<10b54d5dbb3f40307b73ead99.70d312b03e.20181011024234.6b2a4592ab.dce69bc1@mail167.suw121.mcdlv.net>\"\r\n")
	s.readExpectPrefix(`* SEARCH 1`)
	s.readExpectPrefix(`04 OK`)

	s.write("05 UID SEARCH 2:* UNSEEN UNDELETED\r\n")
	s.readExpectPrefix(`* SEARCH 3 4 5`)
	s.readExpectPrefix(`05 OK`)
}

func TestESearch(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	s.write("02 UID SEARCH RETURN (MIN MAX COUNT) 2:* NOT DELETED\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "02") COUNT 3 MIN 3 MAX 5`)
	s.readExpectPrefix(`02 OK`)

	s.write("03 SEARCH RETURN (COUNT) 42:*\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "03") COUNT 0`)
	s.readExpectPrefix(`03 OK`)

	s.write("04 UID SEARCH RETURN (MIN MAX COUNT ALL) 2:*\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "04") COUNT 3 MIN 3 MAX 5 ALL 3:5`)
	s.readExpectPrefix(`04 OK`)

	s.write("05 UID SEARCH RETURN () 2:*\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "05") ALL 3:5`)
	s.readExpectPrefix(`05 OK`)

	s.write("06 SEARCH RETURN () 1:*\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "06") ALL 1:4`)
	s.readExpectPrefix(`06 OK`)

	s.write("07 SEARCH RETURN (MIN MAX COUNT ALL) 1:* deleted\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "07") COUNT 0`)
	s.readExpectPrefix(`07 OK`)

	s.write("08 SEARCH RETURN (MIN COUNT) 1:* flagged\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "08") COUNT 1 MIN 1`)
	s.readExpectPrefix(`08 OK`)

	s.write("09 UID SEARCH RETURN (ALL) BEFORE 18-Dec-1997\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "09")`)
	s.readExpectPrefix(`09 OK`)

	tomorrow := time.Now().AddDate(0, 0, 2).Format("02-Jan-2006")
	s.write("10 UID SEARCH RETURN (ALL) BEFORE " + tomorrow + "\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "10") ALL 1,3:5`)
	s.readExpectPrefix(`10 OK`)

	today := time.Now().Format("02-Jan-2006")
	s.write("10b UID SEARCH RETURN (ALL) ON " + today + "\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "10b") ALL 1,3:5`)
	s.readExpectPrefix(`10b OK`)

	yesterday := time.Now().AddDate(0, 0, -1).Format("02-Jan-2006")
	s.write("11 UID SEARCH RETURN (ALL) SINCE " + yesterday + "\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "11") ALL 1,3:5`)
	s.readExpectPrefix(`11 OK`)

	s.write("12 UID SEARCH RETURN (ALL) OLD\r\n")
	s.readExpectPrefix(`* ESEARCH (TAG "12") ALL 1,3:5`)
	s.readExpectPrefix(`12 OK`)
}

func TestUIDExpunge(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	s.write("02 UID STORE 1:4 +FLAGS.SILENT (\\Deleted)\r\n")
	s.readExpectPrefix(`02 OK`)

	s.write("03 UID EXPUNGE 3,9\r\n")
	s.readExpectPrefix(`* 2 EXPUNGE`)
	s.readExpectPrefix(`03 OK`)

	s.write("04 UID EXPUNGE 1:4\r\n")
	s.readExpectPrefix(`* 1 EXPUNGE`)
	s.readExpectPrefix(`* 1 EXPUNGE`)
	s.readExpectPrefix(`04 OK`)
}

func TestFlags(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	t.Run("STORE_Add", func(t *testing.T) {
		s.t = t
		s.write("02 STORE 1 +FLAGS.SILENT (silent_running)\r\n")
		s.readExpectPrefix(`02 OK`)

		s.write("03 STORE 1 +FLAGS (custom)\r\n")
		s.readExpectPrefix(`* 1 FETCH (FLAGS (\Flagged custom silent_running))`)
		s.readExpectPrefix(`03 OK`)
	})
	t.Run("STORE_Replace", func(t *testing.T) {
		s.t = t
		s.write("02 STORE 1 FLAGS (foo bar \\Deleted)\r\n")
		s.readExpectPrefix(`* 1 FETCH (FLAGS (\Deleted bar foo))`)
		s.readExpectPrefix(`02 OK`)
	})
	t.Run("STORE_Remove", func(t *testing.T) {
		s.t = t

		s.write("02 SEARCH 2 NOT DELETED\r\n")
		s.readExpectPrefix(`* SEARCH 2`)
		s.readExpectPrefix(`02 OK`)

		s.write("03 STORE 2 FLAGS.SILENT (foo bar baz \\Deleted)\r\n")
		s.readExpectPrefix(`03 OK`)

		s.write("04 SEARCH 2 NOT DELETED\r\n")
		s.readExpectPrefix(`04 OK`)

		s.write("05 SEARCH 2 DELETED\r\n")
		s.readExpectPrefix(`* SEARCH 2`)
		s.readExpectPrefix(`05 OK`)

		s.write("06 STORE 2 -FLAGS (foo)\r\n")
		s.readExpectPrefix(`* 2 FETCH (FLAGS (\Deleted bar baz))`)
		s.readExpectPrefix(`06 OK`)
	})
	t.Run("EXPUNGE", func(t *testing.T) {
		s.t = t

		idleInbox := server.Idle(t, "INBOX")
		defer idleInbox.Shutdown()

		s.write("02 EXPUNGE\r\n")
		s.readExpectPrefix(`* 1 EXPUNGE`)
		s.readExpectPrefix(`* 1 EXPUNGE`)
		s.readExpectPrefix(`02 OK`)

		idleInbox.readExpectPrefix(`* 1 EXPUNGE`)
		idleInbox.readExpectPrefix(`* 1 EXPUNGE`)
		idleInbox.readExpectPrefix(`* 2 EXISTS`)

		s.write("03 EXPUNGE\r\n")
		s.readExpectPrefix(`03 OK`)
	})
	t.Run("CLOSE", func(t *testing.T) {
		s.t = t
		s.write("02 STORE 2 FLAGS.SILENT (\\Deleted)\r\n")
		s.readExpectPrefix(`02 OK STORE`)

		idleInbox := server.Idle(t, "INBOX")
		defer idleInbox.Shutdown()

		s.write("03 CLOSE\r\n")
		s.readExpectPrefix(`03 OK CLOSE`)

		idleInbox.readExpectPrefix(`* 2 EXPUNGE`)
		idleInbox.readExpectPrefix(`* 1 EXISTS`)

		s.write("04 SELECT INBOX\r\n")
		s.readExpectPrefix(`* 1 EXISTS`)
		s.readExpectPrefix(`* 0 RECENT`)
		s.readExpectPrefix(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen`)
		s.readExpectPrefix(`* OK [PERMANENTFLAGS (`)
		s.readExpectPrefix(`* OK [HIGHESTMODSEQ`)
		s.readExpectPrefix(`* OK [UNSEEN 1]`)
		s.readExpectPrefix(`* OK [UIDVALIDITY`)
		s.readExpectPrefix(`* OK [UIDNEXT 6]`)
		s.readExpectPrefix(`04 OK`)
	})
}

func TestAppend(t *testing.T, server *TestServer) {
	s := server.OpenSession(t)
	defer s.Shutdown()
	s.read() // initial * OK
	s.login()

	s.selectCmd("INBOX")
	s.write("01 STORE 1:* +FLAGS.SILENT (\\Seen)\r\n")
	s.readExpectPrefix(`01 OK STORE`)

	s.write("04 EXAMINE INBOX\r\n")
	s.readExpectPrefix(`* 4 EXISTS`)
	s.readExpectPrefix(`* 0 RECENT`)
	s.readExpectPrefix(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen`)
	s.readExpectPrefix(`* OK [PERMANENTFLAGS (`)
	s.readExpectPrefix(`* OK [HIGHESTMODSEQ`)
	// UNSEEN is absent, all are now seen
	s.readExpectPrefix(`* OK [UIDVALIDITY`)
	s.readExpectPrefix(`* OK [UIDNEXT 6]`)
	s.readExpectPrefix(`04 OK`)

	// Example from RFC 3501
	msg := strings.Replace(`Date: Mon, 7 Feb 1994 21:52:25 -0800 (PST)
From: Fred Foobar <foobar@Blurdybloop.COM>
Subject: afternoon meeting
To: mooch@owatagu.siam.edu
Message-Id: <B27397-0100000@Blurdybloop.COM>
MIME-Version: 1.0
Content-Type: TEXT/PLAIN; CHARSET=US-ASCII

Hello Joe, do you think we can meet at 3:30 tomorrow?
`, "\n", "\r\n", -1)

	idleInbox := server.Idle(t, "INBOX")
	defer idleInbox.Shutdown()

	s.write("A003 APPEND INBOX ($myflag) {%d}\r\n", len(msg))
	s.readExpectPrefix("+")
	s.write(msg)
	s.write("\r\n")
	s.readExpect(`A003 OK [APPENDUID [0-9]+ 6] APPEND`)

	idleInbox.readExpectPrefix("* 5 EXISTS")

	s.write("04 SELECT INBOX\r\n")
	s.readExpectPrefix(`* 5 EXISTS`) // one more than default
	s.readExpectPrefix(`* 0 RECENT`)
	s.readExpectPrefix(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen`)
	s.readExpectPrefix(`* OK [PERMANENTFLAGS (`)
	s.readExpectPrefix(`* OK [HIGHESTMODSEQ`)
	s.readExpectPrefix(`* OK [UNSEEN 5]`)
	s.readExpectPrefix(`* OK [UIDVALIDITY`)
	s.readExpectPrefix(`* OK [UIDNEXT 7]`)
	s.readExpectPrefix(`04 OK`)

	s.write("05 UID FETCH 6 (BODY[HEADER.FIELDS (From)])\r\n")
	s.readExpectPrefix(`* 5 FETCH (UID 6 BODY[HEADER.FIELDS (From)] {46}`)
	s.readExpectPrefix(`From: Fred Foobar <foobar@Blurdybloop.COM>`)
	s.read()
	s.readExpectPrefix(`)`)
	s.readExpectPrefix(`05 OK`)
}

// TODO: CREATE
// TODO: DELETE

func TestCopy(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	idleArchive := server.Idle(t, "Archive")
	defer idleArchive.Shutdown()

	s.write("01 UID COPY 2:5 Archive\r\n")
	s.readExpect(`\* OK [COPYUID [0-9]+ 3:5 1:3]`)
	s.readExpectPrefix(`01 OK`)

	s.write("02 STATUS Archive (MESSAGES)\r\n")
	s.readExpectPrefix("* STATUS Archive (MESSAGES 3)")
	s.readExpectPrefix(`02 OK`)

	idleArchive.readExpectPrefix("* 3 EXISTS")

	s.selectCmd("Archive")
	s.write("03 FETCH * (BODY[HEADER.FIELDS (Subject)]<0.14>)\r\n")
	s.readExpectPrefix(`* 1 FETCH (BODY[HEADER.FIELDS (Subject)]<0> {2}`)
	s.readExpectPrefix(``)
	s.readExpectPrefix(`)`)
	s.readExpectPrefix(`* 2 FETCH (BODY[HEADER.FIELDS (Subject)]<0> {14}`)
	s.readExpectPrefix(`Subject: Hello)`)
	s.readExpectPrefix(`* 3 FETCH (BODY[HEADER.FIELDS (Subject)]<0> {14}`)
	s.readExpectPrefix(`Subject: Purch)`)
	s.readExpectPrefix(`03 OK`)

	s.write("04 UID COPY 42 INBOX\r\n") // nothing to copy
	s.readExpectPrefix(`04 OK`)
}

func TestMove(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	idleInbox := server.Idle(t, "INBOX")
	defer idleInbox.Shutdown()
	idleArchive := server.Idle(t, "Archive")
	defer idleArchive.Shutdown()

	s.write("01 UID MOVE 2:5 Archive\r\n")
	s.readExpect(`\* OK [COPYUID [0-9]+ 3:5 1:3]`)
	s.readExpectPrefix(`* 2 EXPUNGE`)
	s.readExpectPrefix(`* 2 EXPUNGE`)
	s.readExpectPrefix(`* 2 EXPUNGE`)
	s.readExpectPrefix(`01 OK`)

	idleInbox.readExpectPrefix("* 2 EXPUNGE")
	idleInbox.readExpectPrefix("* 2 EXPUNGE")
	idleInbox.readExpectPrefix("* 2 EXPUNGE")
	idleInbox.readExpectPrefix("* 1 EXISTS")
	idleArchive.readExpectPrefix("* 3 EXISTS")

	s.write("01 SELECT Archive\r\n")
	s.readExpectPrefix(`* 3 EXISTS`)
	s.readExpectPrefix(`* 0 RECENT`)
	s.readExpectPrefix(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen`)
	s.readExpectPrefix(`* OK [PERMANENTFLAGS (`)
	s.readExpectPrefix(`* OK [HIGHESTMODSEQ`)
	s.readExpectPrefix(`* OK [UNSEEN 1]`)
	s.readExpectPrefix(`* OK [UIDVALIDITY`)
	s.readExpectPrefix(`* OK [UIDNEXT 4]`)
	s.readExpectPrefix(`01 OK [READ-WRITE]`)

	s.write("02 STATUS INBOX (MESSAGES)\r\n")
	s.readExpectPrefix("* STATUS INBOX (MESSAGES 1)")
	s.readExpectPrefix(`02 OK`)

	// Test that status updates from MOVE to IDLE-listening
	// connections do not block.
	s.write("1 IDLE\r\n")
	s.readExpectPrefix("+ idling")
	s.write("DONE\r\n")
	s.readExpectPrefix("1 OK")

	s.write("03 MOVE 1,2:3 INBOX\r\n")
	s.readExpect(`\* OK [COPYUID [0-9]+ 1:3 6:8]`)
	s.readExpectPrefix(`* 1 EXPUNGE`)
	s.readExpectPrefix(`* 1 EXPUNGE`)
	s.readExpectPrefix(`* 1 EXPUNGE`)
	s.readExpectPrefix(`* 0 EXISTS`) // because we IDLEd on this session
	s.readExpectPrefix(`03 OK`)

	idleArchive.readExpectPrefix("* 1 EXPUNGE")
	idleArchive.readExpectPrefix("* 1 EXPUNGE")
	idleArchive.readExpectPrefix("* 1 EXPUNGE")
	idleArchive.readExpectPrefix("* 0 EXISTS")
	idleInbox.readExpectPrefix("* 4 EXISTS")

	s.write("02 STATUS INBOX (MESSAGES)\r\n")
	s.readExpectPrefix("* STATUS INBOX (MESSAGES 4)")
	s.readExpectPrefix(`02 OK`)

	s.write("02 STATUS Archive (MESSAGES)\r\n")
	s.readExpectPrefix("* STATUS Archive (MESSAGES 0)")
	s.readExpectPrefix(`02 OK`)

	// TODO s.selectCmd("INBOX")
	// s.write("03 MOVE 2:3,1 Archive\r\n")
}

func TestConcurrency(t *testing.T, server *TestServer) {
	// TODO: why does this work? some of these events should cause sqlite tx failures.
	var sessions []*TestSession
	for i := 0; i < 4; i++ {
		sessions = append(sessions, server.OpenInbox(t))
	}
	defer func() {
		for _, s := range sessions {
			s.Shutdown()
		}
	}()

	var wg sync.WaitGroup
	for si, s := range sessions {
		si, s := si, s
		wg.Add(1)
		go func() {
			for i := 0; i < 50; i++ {
				s.write("%d01 STORE 1 +FLAGS.SILENT (a)\r\n", si)
				s.write("%d02 STORE 1 +FLAGS.SILENT (b)\r\n", si)
				s.write("%d03 STORE 1 +FLAGS.SILENT (c)\r\n", si)
				s.write("%d04 STORE 1 +FLAGS.SILENT (d)\r\n", si)
				s.write("%d11 STORE 1 -FLAGS.SILENT (a)\r\n", si)
				s.write("%d12 STORE 1 -FLAGS.SILENT (b)\r\n", si)
				s.write("%d13 STORE 1 -FLAGS.SILENT (c)\r\n", si)
				s.write("%d14 STORE 1 -FLAGS.SILENT (d)\r\n", si)
				s.readExpectPrefix(fmt.Sprintf(`%d01 OK`, si))
				s.readExpectPrefix(fmt.Sprintf(`%d02 OK`, si))
				s.readExpectPrefix(fmt.Sprintf(`%d03 OK`, si))
				s.readExpectPrefix(fmt.Sprintf(`%d04 OK`, si))
				s.readExpectPrefix(fmt.Sprintf(`%d11 OK`, si))
				s.readExpectPrefix(fmt.Sprintf(`%d12 OK`, si))
				s.readExpectPrefix(fmt.Sprintf(`%d13 OK`, si))
				s.readExpectPrefix(fmt.Sprintf(`%d14 OK`, si))
			}
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestIdle(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	idle1 := server.Idle(t, "INBOX")
	idle1.SetName("idle1")
	idle2 := server.Idle(t, "INBOX")
	idle2.SetName("idle2")
	defer s.Shutdown()
	defer idle1.Shutdown()
	defer idle2.Shutdown()

	idle2.write("DONE\r\n")
	idle2.readExpectPrefix("1 OK")
	idle2.write("1 NOOP\r\n")
	idle2.readExpectPrefix("1 OK")

	msg := "To: crawshaw@spilled.ink\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Hello, world!\r\n"
	s.write("a APPEND INBOX (\\Seen) {%d}\r\n", len(msg))
	s.readExpectPrefix("+")
	s.write(msg)
	s.write("\r\n")
	s.readExpectPrefix("a OK")

	idle1.readExpectPrefix("* 5 EXISTS")
	idle2.write("1 NOOP\r\n")
	idle2.readExpectPrefix("* 5 EXISTS")
	idle2.readExpectPrefix("1 OK")

	s.write("a APPEND INBOX (\\Seen) {%d}\r\n", len(msg))
	s.readExpectPrefix("+")
	s.write(msg)
	s.write("\r\n")
	s.readExpectPrefix("a OK")

	s.write("a IDLE\r\n")
	s.readExpectPrefix("+ idling")
	s.write("DONE\r\n")
	s.readExpectPrefix("a OK")

	s.write("a APPEND INBOX (\\Seen) {%d}\r\n", len(msg))
	s.readExpectPrefix("+")
	s.write(msg)
	s.write("\r\n")
	s.readExpectPrefix("* 7 EXISTS")
	s.readExpectPrefix("a OK")

	idle1.readExpectPrefix("* 6 EXISTS")
	idle1.readExpectPrefix("* 7 EXISTS")

	s.write("a CLOSE\r\n")
	s.readExpectPrefix("a OK")

	s.write("a APPEND INBOX (\\Seen) {%d}\r\n", len(msg))
	s.readExpectPrefix("+")
	s.write(msg)
	s.write("\r\n")
	s.readExpectPrefix("a OK")

	idle1.readExpectPrefix("* 8 EXISTS")

	idle2.write("1 NOOP\r\n")
	idle2.readExpectPrefix("* 8 EXISTS")
	idle2.readExpectPrefix("1 OK")

	// an externally-send message should notify over IDLE
	if err := server.extras.SendMsg(time.Now(), strings.NewReader(msg)); err != nil {
		t.Fatal(err)
	}
	idle1.readExpectPrefix("* 9 EXISTS")
	idle2.write("1 NOOP\r\n")
	idle2.readExpectPrefix("* 9 EXISTS")
	idle2.readExpectPrefix("1 OK")

	// IDLE in authenticated state (done by iOS mail).
	s.write("1 idle\r\n")
	s.readExpectPrefix("+ idling")
	s.write("DONE\r\n")
	s.readExpectPrefix("1 OK")
}

func TestCompress(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	s.Compress()

	s.write("1 NOOP\r\n")
	s.readExpect("1 OK")

	s.write("1 IDLE\r\n")
	s.readExpectPrefix("+ idling")
	s.write("DONE\r\n")
	s.readExpectPrefix("1 OK")
}

func TestXApplePushService(t *testing.T, server *TestServer) {
	server.s.APNS = &imapserver.APNS{
		GatewayAddr: "localhost",
		UID:         "custom-topic",
	}
	defer func() {
		server.s.APNS = nil
	}()

	s := server.OpenSession(t)
	s.read() // initial * OK
	s.login()
	defer s.Shutdown()

	s.write("1 XAPPLEPUSHSERVICE aps-version 2 " +
		"aps-account-id ACC37604-1111-494B-2222-FCA34566717E " +
		"aps-device-token FD3ABAA234203CC2349587349587999BBBEC1CB40AEE23688E2665BBBB2A28D4 " +
		"aps-subtopic com.apple.mobilemail mailboxes (Notes INBOX \"Sent Messages\")\r\n")
	s.readExpectPrefix(`* XAPPLEPUSHSERVICE aps-version "2" aps-topic "custom-topic"`)
	s.readExpectPrefix("1 OK")
}
