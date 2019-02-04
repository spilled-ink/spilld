package imaptest

import (
	"fmt"
	"testing"
)

func TestFetchModSeq(t *testing.T, server *TestServer) {
	s := server.OpenSession(t)
	defer s.Shutdown()
	s.read() // initial * OK
	s.login()
	s.selectCmd("INBOX")

	s.write("01 FETCH 1:2 (UID)\r\n")
	s.readExpectPrefix(`* 1 FETCH (UID 1)`)
	s.readExpectPrefix(`* 2 FETCH (UID 3)`)
	s.readExpectPrefix(`01 OK`)

	var highestModSeq, modSeq1, modSeq2 int64
	s.write("02 FETCH 1:2 (UID MODSEQ)\r\n")
	if _, err := fmt.Sscanf(s.read(), `* OK [HIGHESTMODSEQ %d]`, &highestModSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscanf(s.read(), `* 1 FETCH (UID 1 MODSEQ (%d))`, &modSeq1); err != nil {
		t.Fatal(err)
	}
	if modSeq1 > highestModSeq {
		t.Errorf("msg1 mod-seq %d greater than highest mod-seq %d", modSeq1, highestModSeq)
	}
	if _, err := fmt.Sscanf(s.read(), `* 2 FETCH (UID 3 MODSEQ (%d))`, &modSeq2); err != nil {
		t.Fatal(err)
	}
	if modSeq2 > highestModSeq {
		t.Errorf("msg2 mod-seq %d greater than highest mod-seq %d", modSeq2, highestModSeq)
	}
	s.readExpectPrefix(`02 OK`)

	var highestModSeqStatus int64
	s.write("02 STATUS INBOX (HIGHESTMODSEQ)\r\n")
	if _, err := fmt.Sscanf(s.read(), "* STATUS INBOX (HIGHESTMODSEQ %d)\r\n", &highestModSeqStatus); err != nil {
		t.Fatalf("STATUS INBOX: %v", err)
	}
	if highestModSeqStatus != highestModSeq {
		t.Errorf("initial HIGHESTMODSEQ %d does not match STATUS value %d", highestModSeq, highestModSeqStatus)
	}
	s.readExpectPrefix(`02 OK`)

	// Returned FETCH values include MODSEQ now that we sent a MODSEQ message.
	var modSeq3 int64
	s.write("03 STORE 1 +FLAGS (\\Deleted)\r\n")
	if _, err := fmt.Sscanf(s.read(), "* 1 FETCH (MODSEQ (%d) FLAGS (\\Deleted \\Flagged))\r\n", &modSeq3); err != nil {
		t.Fatalf("STORE FETCH: %v", err)
	}
	s.readExpectPrefix(`03 OK`)
	if highestModSeq > modSeq3 {
		t.Errorf("new mod-seq %d less than old highest mod-seq %d", modSeq3, highestModSeq)
	}

	(func() {
		s := server.OpenSession(t)
		defer s.Shutdown()
		s.read() // initial * OK
		s.login()

		s.write("01 SELECT INBOX (CONDSTORE)\r\n")
		s.readExpectPrefix(`* 4 EXISTS`)
		s.readExpectPrefix(`* 0 RECENT`)
		s.readExpectPrefix(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen`)
		s.readExpectPrefix(`* OK [PERMANENTFLAGS (`)
		s.readExpectPrefix(`* OK [HIGHESTMODSEQ`)
		s.readExpectPrefix(`* OK [UNSEEN 1]`)
		s.readExpectPrefix(`* OK [UIDVALIDITY`)
		s.readExpectPrefix(`* OK [UIDNEXT 6]`)
		s.readExpectPrefix(`01 OK [READ-WRITE] SELECT completed, CONDSTORE enabled`)
	}())

	s.write("05 SEARCH MODSEQ %d\r\n", modSeq3)
	s.readExpectPrefix(fmt.Sprintf(`* SEARCH 1 (MODSEQ %d)`, modSeq3))
	s.readExpectPrefix(`05 OK`)

	s.write("06 SEARCH MODSEQ 1\r\n")
	s.readExpectPrefix(fmt.Sprintf(`* SEARCH 1 2 3 4 (MODSEQ %d)`, modSeq3))
	s.readExpectPrefix(`06 OK`)

	s.write("07 SEARCH RETURN (MIN) MODSEQ 1\r\n")
	s.readExpectPrefix(fmt.Sprintf(`* ESEARCH (TAG "07") MIN 1 MODSEQ %d`, modSeq3))
	s.readExpectPrefix(`07 OK`)

	var seq4ModSeq int64
	s.write("08pre FETCH 4 (MODSEQ)\r\n")
	if _, err := fmt.Sscanf(s.read(), `* 4 FETCH (MODSEQ (%d))`, &seq4ModSeq); err != nil {
		t.Fatal(err)
	}
	s.readExpectPrefix(`08pre OK`)
	s.write("08a SEARCH RETURN (MAX) MODSEQ 1\r\n")
	s.readExpectPrefix(fmt.Sprintf(`* ESEARCH (TAG "08a") MAX 4 MODSEQ %d`, seq4ModSeq))
	s.readExpectPrefix(`08a OK`)
	s.write("08b SEARCH RETURN (MIN MAX) MODSEQ 1\r\n")
	s.readExpectPrefix(fmt.Sprintf(`* ESEARCH (TAG "08b") MIN 1 MAX 4 MODSEQ %d`, modSeq3))
	s.readExpectPrefix(`08b OK`) // modSeq3 > seq4ModSeq
	s.write("08c SEARCH RETURN (COUNT MAX) MODSEQ 1\r\n")
	s.readExpectPrefix(fmt.Sprintf(`* ESEARCH (TAG "08c") COUNT 4 MAX 4 MODSEQ %d`, modSeq3))
	s.readExpectPrefix(`08c OK`) // modSeq3 is top modseq of all messages
	s.write("08d SEARCH RETURN () MODSEQ 1\r\n")
	s.readExpectPrefix(fmt.Sprintf(`* ESEARCH (TAG "08d") ALL 1:4 MODSEQ %d`, modSeq3))
	s.readExpectPrefix(`08d OK`)
	s.write("08e SEARCH RETURN (COUNT MIN) 4 MODSEQ 1\r\n")
	s.readExpectPrefix(fmt.Sprintf(`* ESEARCH (TAG "08e") COUNT 1 MIN 4 MODSEQ %d`, seq4ModSeq))
	s.readExpectPrefix(`08e OK`)
}

func TestUnchangedSince(t *testing.T, server *TestServer) {
	s := server.OpenSession(t)
	defer s.Shutdown()
	s.read() // initial * OK
	s.login()

	var initModSeq int64

	s.write("01 SELECT INBOX\r\n")
	s.readExpectPrefix(`* 4 EXISTS`)
	s.readExpectPrefix(`* 0 RECENT`)
	s.readExpectPrefix(`* FLAGS (\Answered \Flagged \Draft \Deleted \Seen`)
	s.readExpectPrefix(`* OK [PERMANENTFLAGS (`)
	if _, err := fmt.Sscanf(s.read(), "* OK [HIGHESTMODSEQ %d]\r\n", &initModSeq); err != nil {
		t.Fatal(err)
	}
	s.readExpectPrefix(`* OK [UNSEEN 1]`)
	s.readExpectPrefix(`* OK [UIDVALIDITY`)
	s.readExpectPrefix(`* OK [UIDNEXT 6]`)
	s.readExpectPrefix(`01 OK`)

	var modSeq1 int64
	s.write("02 UID STORE 1 (UNCHANGEDSINCE %d) +FLAGS.SILENT (\\Seen)\r\n", initModSeq)
	s.readExpectPrefix("* OK [HIGHESTMODSEQ")
	if _, err := fmt.Sscanf(s.read(), "* 1 FETCH (UID 1 MODSEQ (%d))\r\n", &modSeq1); err != nil {
		t.Fatal(err)
	}
	s.readExpectPrefix(`02 OK Conditional`)
	if initModSeq >= modSeq1 {
		t.Errorf("first store did not increase modseq: initModSeq=%d, modSeq1=%d", initModSeq, modSeq1)
	}

	var modSeq2 int64
	s.write("03 STORE 2 (UNCHANGEDSINCE %d) +FLAGS.SILENT (\\Seen)\r\n", initModSeq)
	if _, err := fmt.Sscanf(s.read(), "* 2 FETCH (MODSEQ (%d))\r\n", &modSeq2); err != nil {
		t.Fatal(err)
	}
	s.readExpectPrefix(`03 OK Conditional`)
	if modSeq1 >= modSeq2 {
		t.Errorf("second store did not increase modseq: modSeq1=%d, modSeq2=%d", modSeq1, modSeq2)
	}

	// An older modseq can be used to update just records
	// in a mailbox with the older modseq.
	var modSeq3 int64
	s.write("04 UID STORE 1 (UNCHANGEDSINCE %d) +FLAGS (again)\r\n", modSeq1)
	if _, err := fmt.Sscanf(s.read(), "* 1 FETCH (UID 1 MODSEQ (%d) FLAGS (\\Flagged \\Seen again))\r\n", &modSeq3); err != nil {
		t.Fatal(err)
	}
	s.readExpectPrefix(`04 OK Conditional`)
	if modSeq2 >= modSeq3 {
		t.Errorf("store did not increase modseq: modSeq2=%d, modSeq3=%d", modSeq2, modSeq3)
	}

	// An older modseq on a message will fail.
	s.write("05 UID STORE 1 (UNCHANGEDSINCE %d) FLAGS (\\Seen)\r\n", modSeq1)
	// TODO: RFC 7162 seems to suggests best practice is to return some FETCH data on failure?
	s.readExpectPrefix(`05 OK [MODIFIED 1] Conditional STORE failed`)

	// An older modseq on a message will succeed if the flag change
	// does not conflict with other changes.
	// And if it does not change the server state, the server should
	// not increment the mod-sequence.
	var modSeq4 int64
	s.write("06 UID STORE 1 (UNCHANGEDSINCE %d) +FLAGS (again)\r\n", modSeq1)
	if _, err := fmt.Sscanf(s.read(), "* 1 FETCH (UID 1 MODSEQ (%d) FLAGS (\\Flagged \\Seen again))\r\n", &modSeq4); err != nil {
		t.Fatal(err)
	}
	s.readExpectPrefix(`06 OK Conditional STORE completed`)
	if modSeq4 != modSeq3 {
		t.Errorf("no-op flag change should not change modseq, modSeq3=%d, modSeq4=%d", modSeq3, modSeq4)
	}

	// An older modseq on a message will succeed
	// if the flag change does not conflict with other changes.
	// It will increase the mod-sequence.
	var modSeq5 int64
	s.write("07 UID STORE 1 (UNCHANGEDSINCE %d) -FLAGS (\\Flagged)\r\n", modSeq1)
	if _, err := fmt.Sscanf(s.read(), "* 1 FETCH (UID 1 MODSEQ (%d) FLAGS (\\Seen again))\r\n", &modSeq5); err != nil {
		t.Fatal(err)
	}
	s.readExpectPrefix(`07 OK Conditional STORE completed`)
	if modSeq4 >= modSeq5 {
		t.Errorf("store did not increase modseq: modSeq4=%d, modSeq5=%d", modSeq4, modSeq5)
	}

	s.write("08 UID FETCH 1 (FLAGS UID) (CHANGEDSINCE %d)\r\n", modSeq5)
	s.readExpectPrefix("08 OK UID FETCH completed")
	s.write("09 NOOP\r\n")
	s.readExpectPrefix("09 OK") // make sure no error message follows 08 UID FETCH
}
