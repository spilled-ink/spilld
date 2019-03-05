package imaptest

import (
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestFetch(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	t.Run("FLAGS", func(t *testing.T) {
		s.t = t
		s.write("02 UID FETCH 1,3:4 (UID FLAGS)\r\n")
		s.readExpectPrefix("* 1 FETCH (UID 1 FLAGS (\\Flagged))")
		s.readExpectPrefix("* 2 FETCH (UID 3 FLAGS (\\Junk))")
		s.readExpectPrefix("* 3 FETCH (UID 4 FLAGS (\\Junk))")
		s.readExpectPrefix(`02 OK`)
	})
	t.Run("RFC822.SIZE", func(t *testing.T) {
		s.t = t
		s.write("02 UID FETCH 1,3,4 (RFC822.SIZE)\r\n")
		s.readExpectPrefix("* 1 FETCH (RFC822.SIZE 1473573 UID 1)")
		s.readExpectPrefix("* 2 FETCH (RFC822.SIZE 598 UID 3)")
		s.readExpectPrefix("* 3 FETCH (RFC822.SIZE 468 UID 4)")
		s.readExpectPrefix(`02 OK`)
	})
	t.Run("BODYSTRUCTURE", func(t *testing.T) {
		s.t = t
		s.write("02 UID FETCH 1,3:4 (BODYSTRUCTURE)\r\n")
		// testdata/msg1.eml:
		s.readExpect(`BODYSTRUCTURE .* \(image png \(name fetchasset4\) "<fetchasset4>" NIL base64 141086\) \(image png \(name fetchasset5\) "<fetchasset5>" NIL base64 309226\) .*`)
		// testdata/msg3.eml:
		s.readExpect(`BODYSTRUCTURE \(\(text plain .* 11 1\) \(text html .* 17 1\) \(text rich .* 135 4\) ALTERNATIVE .*boundary ".PM0QwL`)
		// testdata/msg4.eml:
		s.readExpect(`BODYSTRUCTURE \(text plain .* quoted-printable 230 7\)`)
		s.readExpectPrefix(`02 OK`)
	})
	t.Run("ENVELOPE", func(t *testing.T) {
		s.t = t
		s.write("02 UID FETCH 1 (ENVELOPE)\r\n")
		// TODO: is UTF-7 encoding the subject line right?
		// That's not how MIME header unicode encoding works.
		s.readExpect(`\(ENVELOPE \(".*Oct 2018 .* Events\&AKDYPd6A-" .* organizers spaceapps.nyc\) .* \("David Crawshaw" NIL david zentus.com\) .* "<10b5.*mcdlv.net>"\) UID 1\)`)
		s.readExpectPrefix(`02 OK`)
	})
	t.Run("INTERNALDATE", func(t *testing.T) {
		s.t = t
		s.write("02 UID FETCH 1 (INTERNALDATE)\r\n")
		s.readExpectPrefix(`* 1 FETCH (INTERNALDATE "` + time.Now().Format("02-Jan-2006"))
		s.readExpectPrefix(`02 OK`)
	})
}

func TestFetchBody(t *testing.T, server *TestServer) {
	s := server.OpenInbox(t)
	defer s.Shutdown()

	t.Run("msg4 BODY[1]", func(t *testing.T) {
		s.t = t
		s.write("02 UID FETCH 4 (BODY[1])\r\n")
		s.readExpectPrefix(`* 3 FETCH (UID 4 BODY[1] {230}`)

		b := make([]byte, 230)
		if _, err := io.ReadFull(s.br, b); err != nil {
			t.Fatal("could not read literal: %", err)
		}
		s.readExpectPrefix(`)`)
		s.readExpectPrefix(`02 OK`)

		if got := string(b); !strings.Contains(got, "venerable quoted-printabl=\r\ne encoding") {
			t.Error("msg 4 body not quoted-printable encoded")
		}
	})

	t.Run("msg4 BODY[]", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 3 (BODY[])\r\n")
		s.readExpectPrefix(`* 3 FETCH (BODY[] {468}`)
		b := make([]byte, 468)
		if _, err := io.ReadFull(s.br, b); err != nil {
			t.Fatal("could not read literal: %", err)
		}
		s.readExpectPrefix(`)`)
		s.readExpectPrefix(`02 OK`)

		if got := string(b); !strings.Contains(got, "To: david") {
			t.Error("msg 4 missing headers")
		}
		if got := string(b); !strings.Contains(got, "venerable quoted-printabl=\r\ne encoding") {
			t.Error("msg 4 body not quoted-printable encoded")
		}
	})

	t.Run("msg1 BODY.PEEK[2.1]<0.25>", func(t *testing.T) {
		s.t = t

		s.write("02 FETCH 1 (FLAGS BODY.PEEK[2.1]<0.25>)\r\n")
		s.readExpectPrefix(`* 1 FETCH (FLAGS (\Flagged) BODY[2.1]<0> {25}`)
		s.readExpectPrefix(`<!doctype html>`)
		s.readExpectPrefix(`<html>`)
		s.readExpectPrefix(`)`)
		s.readExpectPrefix(`02 OK`)

		s.write("03 FETCH 1 (FLAGS)\r\n")
		s.readExpectPrefix(`* 1 FETCH (FLAGS (\Flagged))`) // not \Seen
		s.readExpectPrefix(`03 OK`)
	})

	t.Run("msg1 BODY[1]<0.25>", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (FLAGS BODY[1]<0.25>)\r\n")
		s.readExpectPrefix(`* 1 FETCH (FLAGS (\Flagged) BODY[1]<0> {25}`)
		s.readExpectPrefix(`A Journey to the Stars by)`)
		s.readExpectPrefix(`02 OK`)

		s.write("03 FETCH 1 (FLAGS)\r\n")
		s.readExpectPrefix(`* 1 FETCH (FLAGS (\Flagged \Seen))`) // \Seen
		s.readExpectPrefix(`03 OK`)
	})

	t.Run("msg1 BODY.PEEK[2.14]", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (BODY.PEEK[2.14])\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[2.14] {48}`)
		s.readExpectPrefix(`R0lGODdhAQABAIAAAP///////ywAAAAAAQABAAACAkQBADs=)`)
		s.readExpectPrefix(`02 OK`)
	})

	t.Run("msg1 BODY.PEEK[2.14.TEXT]", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (BODY.PEEK[2.14.TEXT])\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[2.14.TEXT] {48}`)
		s.readExpectPrefix(`R0lGODdhAQABAIAAAP///////ywAAAAAAQABAAACAkQBADs=)`)
		s.readExpectPrefix(`02 OK`)
	})

	t.Run("msg1 BODY[2.14]<10.15>", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (BODY.PEEK[2.14]<10.15>)\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[2.14]<10> {15}`)
		s.readExpectPrefix(`ABAIAAAP///////)`)
		s.readExpectPrefix(`02 OK`)
	})

	t.Run("msg1 BODY[HEADER]", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (BODY[HEADER])\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[HEADER] {9011}`)
		b := make([]byte, 9011)
		if _, err := io.ReadFull(s.br, b); err != nil {
			t.Fatal("could not read literal: %", err)
		}
		s.readExpectPrefix(`)`)
		s.readExpectPrefix(`02 OK`)

		m := regexp.MustCompile(`.*(Subject: .*?\r\n)`).FindSubmatch(b)
		got := string(m[1])

		if !strings.Contains(got, "Subject: Upcoming Space Apps Bootcamp Events") {
			t.Error("headers are missing subject")
		}

		//want := "Subject: Upcoming Space Apps Bootcamp Events 🚀\r\n"
		//t.Logf("%q", got)
		//t.Logf("%q", want)
		//if !strings.Contains(got, want) {
		//	t.Error("header subject is missing emoji")
		//}
	})

	t.Run("msg1 BODY[HEADER.FIELDS (To From MIME-Version)]", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (BODY[HEADER.FIELDS (To From MIME-Version)])\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[HEADER.FIELDS (To From MIME-Version)] {120}`)
		s.readExpectPrefix(`To: David Crawshaw <david@zentus.com>`)
		s.readExpectPrefix(`From: Space Apps NYC Organizers <organizers@spaceapps.nyc>`)
		s.readExpectPrefix(`MIME-Version: 1.0`)
		s.read()
		s.readExpectPrefix(`)`)
		s.readExpectPrefix(`02 OK`)
	})

	t.Run("msg1 BODY[HEADER.FIELDS.NOT (To)]", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (BODY[HEADER.FIELDS.NOT (To)])\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[HEADER.FIELDS.NOT (To)] {8972}`)
		b := make([]byte, 8972)
		if _, err := io.ReadFull(s.br, b); err != nil {
			t.Fatal("could not read literal: %", err)
		}
		s.readExpectPrefix(`)`)
		s.readExpectPrefix(`02 OK`)

		if regexp.MustCompile(`.*(\r\nTo: .*?\r\n)`).Match(b) {
			t.Errorf("found To: header expected to be absent")
		}
	})

	t.Run("msg1 BODY[2.14.HEADER]", func(t *testing.T) {
		s.t = t
		s.write("02 FETCH 1 (BODY[2.14.HEADER])\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[2.14.HEADER] {`)
		s.readExpectPrefix(`Content-Disposition: inline; filename="fetchasset12"`)
		s.readExpectPrefix(`Content-ID: <fetchasset12>`)
		s.readExpectPrefix(`Content-Transfer-Encoding: base64`)
		s.readExpectPrefix(`Content-Type: image/gif`)
		s.read()
		s.readExpectPrefix(`)`)
		s.readExpectPrefix(`02 OK`)
	})

	//t.Run("msg1 BODY[2.14.HEADER.FIELDS (Content-Type)]", func(t *testing.T) {
	//	s.t = t
	//	s.write("02 FETCH 1 (BODY[2.14.HEADER.FIELDS (Content-Type)])\r\n")
	//	s.readExpectPrefix(`* 1 FETCH BODY[2.14.HEADER.FIELDS (Content-Type)] {120}`)
	//	s.readExpectPrefix(`MIME-Version: 1.0`)
	//	s.read()
	//	s.readExpectPrefix(`)`)
	//	s.readExpectPrefix(`02 OK`)
	//})
	//s.t = t

	//t.Run("msg1 BODY[2.14.HEADER.FIELDS.NOT (Content-Type Content-Disposition)]", func(t *testing.T) {
	//	s.t = t
	//	s.write("02 FETCH 1 (BODY[2.14.HEADER.FIELDS.NOT (Content-Type Content-Disposition)])\r\n")
	//	s.readExpectPrefix(`* 1 FETCH BODY[2.14.HEADER.FIELDS.NOT (Content-Type Content-Disposition)] {120}`)
	//	s.readExpectPrefix(`Content-ID: <fetchasset12>`)
	//	s.readExpectPrefix(`Content-Transfer-Encoding: base64`)
	//	s.read()
	//	s.readExpectPrefix(`)`)
	//	s.readExpectPrefix(`02 OK`)
	//})
	//s.t = t

	// TODO: 02 FETCH 1 (RFC822.HEADER)
	// TODO: 02 FETCH 1 (RFC822.TEXT)
}

/*
// go test -test.cpuprofile=imapserver.prof -test.benchtime=5s -test.bench=.* -test.run=nothing ./email/imapserver
// go tool pprof -pdf imapserver.prof
func BenchmarkFetchBody(b *testing.B) {
	s := newTestServer(b)
	//s.s.Filer.DefaultBufferMemSize = 1 << 21
	defer s.shutdown()
	s.read() // initial * OK
	s.login()
	s.selectCmd("INBOX")
	s.s.Logf = func(format string, v ...interface{}) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.connLog.Reset()
		s.write("02 FETCH 1 (BODY[1]<0.25>)\r\n")
		s.readExpectPrefix(`* 1 FETCH (BODY[1]<0> {25}`)
		s.readExpectPrefix(`A Journey to the Stars by)`)
		s.readExpectPrefix(`02 OK`)
	}
}

*/
