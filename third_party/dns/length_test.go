package dns

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestCompressLength(t *testing.T) {
	m := new(Msg)
	m.SetQuestion("miek.nl.", TypeMX)
	ul := m.Len()
	m.Compress = true
	if ul != m.Len() {
		t.Fatalf("should be equal")
	}
}

// Does the predicted length match final packed length?
func TestMsgCompressLength(t *testing.T) {
	makeMsg := func(question string, ans, ns, e []RR) *Msg {
		msg := new(Msg)
		msg.SetQuestion(Fqdn(question), TypeANY)
		msg.Answer = append(msg.Answer, ans...)
		msg.Ns = append(msg.Ns, ns...)
		msg.Extra = append(msg.Extra, e...)
		msg.Compress = true
		return msg
	}

	name1 := "12345678901234567890123456789012345.12345678.123."
	rrA := testRR(name1 + " 3600 IN A 192.0.2.1")
	rrMx := testRR(name1 + " 3600 IN MX 10 " + name1)
	tests := []*Msg{
		makeMsg(name1, []RR{rrA}, nil, nil),
		makeMsg(name1, []RR{rrMx, rrMx}, nil, nil)}

	for _, msg := range tests {
		predicted := msg.Len()
		buf, err := msg.Pack()
		if err != nil {
			t.Error(err)
		}
		if predicted < len(buf) {
			t.Errorf("predicted compressed length is wrong: predicted %s (len=%d) %d, actual %d",
				msg.Question[0].Name, len(msg.Answer), predicted, len(buf))
		}
	}
}

func TestMsgLength(t *testing.T) {
	makeMsg := func(question string, ans, ns, e []RR) *Msg {
		msg := new(Msg)
		msg.Compress = true
		msg.SetQuestion(Fqdn(question), TypeANY)
		msg.Answer = append(msg.Answer, ans...)
		msg.Ns = append(msg.Ns, ns...)
		msg.Extra = append(msg.Extra, e...)
		return msg
	}

	name1 := "12345678901234567890123456789012345.12345678.123."
	rrA := testRR(name1 + " 3600 IN A 192.0.2.1")
	rrMx := testRR(name1 + " 3600 IN MX 10 " + name1)
	tests := []*Msg{
		makeMsg(name1, []RR{rrA}, nil, nil),
		makeMsg(name1, []RR{rrMx, rrMx}, nil, nil)}

	for _, msg := range tests {
		predicted := msg.Len()
		buf, err := msg.Pack()
		if err != nil {
			t.Error(err)
		}
		if predicted < len(buf) {
			t.Errorf("predicted length is wrong: predicted %s (len=%d), actual %d",
				msg.Question[0].Name, predicted, len(buf))
		}
	}
}

func TestCompressionLenSearchInsert(t *testing.T) {
	c := make(map[string]struct{})
	compressionLenSearch(c, "example.com", 12)
	if _, ok := c["example.com"]; !ok {
		t.Errorf("bad example.com")
	}
	if _, ok := c["com"]; !ok {
		t.Errorf("bad com")
	}

	// Test boundaries
	c = make(map[string]struct{})
	// foo label starts at 16379
	// com label starts at 16384
	compressionLenSearch(c, "foo.com", 16379)
	if _, ok := c["foo.com"]; !ok {
		t.Errorf("bad foo.com")
	}
	// com label is accessible
	if _, ok := c["com"]; !ok {
		t.Errorf("bad com")
	}

	c = make(map[string]struct{})
	// foo label starts at 16379
	// com label starts at 16385 => outside range
	compressionLenSearch(c, "foo.com", 16380)
	if _, ok := c["foo.com"]; !ok {
		t.Errorf("bad foo.com")
	}
	// com label is NOT accessible
	if _, ok := c["com"]; ok {
		t.Errorf("bad com")
	}

	c = make(map[string]struct{})
	compressionLenSearch(c, "example.com", 16375)
	if _, ok := c["example.com"]; !ok {
		t.Errorf("bad example.com")
	}
	// com starts AFTER 16384
	if _, ok := c["com"]; !ok {
		t.Errorf("bad com")
	}

	c = make(map[string]struct{})
	compressionLenSearch(c, "example.com", 16376)
	if _, ok := c["example.com"]; !ok {
		t.Errorf("bad example.com")
	}
	// com starts AFTER 16384
	if _, ok := c["com"]; ok {
		t.Errorf("bad com")
	}
}

func TestCompressionLenSearch(t *testing.T) {
	c := make(map[string]struct{})
	compressed, ok := compressionLenSearch(c, "a.b.org.", maxCompressionOffset)
	if compressed != 0 || ok {
		t.Errorf("Failed: compressed:=%d, ok:=%v", compressed, ok)
	}
	c["org."] = struct{}{}
	compressed, ok = compressionLenSearch(c, "a.b.org.", maxCompressionOffset)
	if compressed != 4 || !ok {
		t.Errorf("Failed: compressed:=%d, ok:=%v", compressed, ok)
	}
	c["b.org."] = struct{}{}
	compressed, ok = compressionLenSearch(c, "a.b.org.", maxCompressionOffset)
	if compressed != 2 || !ok {
		t.Errorf("Failed: compressed:=%d, ok:=%v", compressed, ok)
	}
	// Not found long compression
	c["x.b.org."] = struct{}{}
	compressed, ok = compressionLenSearch(c, "a.b.org.", maxCompressionOffset)
	if compressed != 2 || !ok {
		t.Errorf("Failed: compressed:=%d, ok:=%v", compressed, ok)
	}
	// Found long compression
	c["a.b.org."] = struct{}{}
	compressed, ok = compressionLenSearch(c, "a.b.org.", maxCompressionOffset)
	if compressed != 0 || !ok {
		t.Errorf("Failed: compressed:=%d, ok:=%v", compressed, ok)
	}
}

func TestMsgLengthCompressionMalformed(t *testing.T) {
	// SOA with empty hostmaster, which is illegal
	soa := &SOA{Hdr: RR_Header{Name: ".", Rrtype: TypeSOA, Class: ClassINET, Ttl: 12345},
		Ns:      ".",
		Mbox:    "",
		Serial:  0,
		Refresh: 28800,
		Retry:   7200,
		Expire:  604800,
		Minttl:  60}
	m := new(Msg)
	m.Compress = true
	m.Ns = []RR{soa}
	m.Len() // Should not crash.
}

func TestMsgCompressLength2(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion(Fqdn("bliep."), TypeANY)
	msg.Answer = append(msg.Answer, &SRV{Hdr: RR_Header{Name: "blaat.", Rrtype: 0x21, Class: 0x1, Ttl: 0x3c}, Port: 0x4c57, Target: "foo.bar."})
	msg.Extra = append(msg.Extra, &A{Hdr: RR_Header{Name: "foo.bar.", Rrtype: 0x1, Class: 0x1, Ttl: 0x3c}, A: net.IP{0xac, 0x11, 0x0, 0x3}})
	predicted := msg.Len()
	buf, err := msg.Pack()
	if err != nil {
		t.Error(err)
	}
	if predicted != len(buf) {
		t.Errorf("predicted compressed length is wrong: predicted %s (len=%d) %d, actual %d",
			msg.Question[0].Name, len(msg.Answer), predicted, len(buf))
	}
}

func TestMsgCompressLengthLargeRecords(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("my.service.acme.", TypeSRV)
	j := 1
	for i := 0; i < 250; i++ {
		target := fmt.Sprintf("host-redis-1-%d.test.acme.com.node.dc1.consul.", i)
		msg.Answer = append(msg.Answer, &SRV{Hdr: RR_Header{Name: "redis.service.consul.", Class: 1, Rrtype: TypeSRV, Ttl: 0x3c}, Port: 0x4c57, Target: target})
		msg.Extra = append(msg.Extra, &CNAME{Hdr: RR_Header{Name: target, Class: 1, Rrtype: TypeCNAME, Ttl: 0x3c}, Target: fmt.Sprintf("fx.168.%d.%d.", j, i)})
	}
	predicted := msg.Len()
	buf, err := msg.Pack()
	if err != nil {
		t.Error(err)
	}
	if predicted != len(buf) {
		t.Fatalf("predicted compressed length is wrong: predicted %s (len=%d) %d, actual %d", msg.Question[0].Name, len(msg.Answer), predicted, len(buf))
	}
}

func compressionMapsEqual(a map[string]struct{}, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}

	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}

	return true
}

func compressionMapsDifference(a map[string]struct{}, b map[string]int) string {
	var s strings.Builder

	var c int
	fmt.Fprintf(&s, "length compression map (%d):", len(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			if c > 0 {
				s.WriteString(",")
			}

			fmt.Fprintf(&s, " missing %q", k)
			c++
		}
	}

	c = 0
	fmt.Fprintf(&s, "\npack compression map (%d):", len(b))
	for k := range a {
		if _, ok := b[k]; !ok {
			if c > 0 {
				s.WriteString(",")
			}

			fmt.Fprintf(&s, " missing %q", k)
			c++
		}
	}

	return s.String()
}

func TestCompareCompressionMapsForANY(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("a.service.acme.", TypeANY)
	// Be sure to have more than 14bits
	for i := 0; i < 2000; i++ {
		target := fmt.Sprintf("host.app-%d.x%d.test.acme.", i%250, i)
		msg.Answer = append(msg.Answer, &AAAA{Hdr: RR_Header{Name: target, Rrtype: TypeAAAA, Class: ClassINET, Ttl: 0x3c}, AAAA: net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(i / 255), byte(i % 255)}})
		msg.Answer = append(msg.Answer, &A{Hdr: RR_Header{Name: target, Rrtype: TypeA, Class: ClassINET, Ttl: 0x3c}, A: net.IP{127, 0, byte(i / 255), byte(i % 255)}})
		if msg.Len() > 16384 {
			break
		}
	}
	for labelSize := 0; labelSize < 63; labelSize++ {
		msg.SetQuestion(fmt.Sprintf("a%s.service.acme.", strings.Repeat("x", labelSize)), TypeANY)

		compressionFake := make(map[string]struct{})
		lenFake := msgLenWithCompressionMap(msg, compressionFake)

		compressionReal := make(map[string]int)
		buf, err := msg.packBufferWithCompressionMap(nil, compressionMap{ext: compressionReal}, true)
		if err != nil {
			t.Fatal(err)
		}
		if lenFake != len(buf) {
			t.Fatalf("padding= %d ; Predicted len := %d != real:= %d", labelSize, lenFake, len(buf))
		}
		if !compressionMapsEqual(compressionFake, compressionReal) {
			t.Fatalf("padding= %d ; Fake Compression Map != Real Compression Map\n%s", labelSize, compressionMapsDifference(compressionFake, compressionReal))
		}
	}
}

func TestCompareCompressionMapsForSRV(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("a.service.acme.", TypeSRV)
	// Be sure to have more than 14bits
	for i := 0; i < 2000; i++ {
		target := fmt.Sprintf("host.app-%d.x%d.test.acme.", i%250, i)
		msg.Answer = append(msg.Answer, &SRV{Hdr: RR_Header{Name: "redis.service.consul.", Class: ClassINET, Rrtype: TypeSRV, Ttl: 0x3c}, Port: 0x4c57, Target: target})
		msg.Extra = append(msg.Extra, &A{Hdr: RR_Header{Name: target, Rrtype: TypeA, Class: ClassINET, Ttl: 0x3c}, A: net.IP{127, 0, byte(i / 255), byte(i % 255)}})
		if msg.Len() > 16384 {
			break
		}
	}
	for labelSize := 0; labelSize < 63; labelSize++ {
		msg.SetQuestion(fmt.Sprintf("a%s.service.acme.", strings.Repeat("x", labelSize)), TypeAAAA)

		compressionFake := make(map[string]struct{})
		lenFake := msgLenWithCompressionMap(msg, compressionFake)

		compressionReal := make(map[string]int)
		buf, err := msg.packBufferWithCompressionMap(nil, compressionMap{ext: compressionReal}, true)
		if err != nil {
			t.Fatal(err)
		}
		if lenFake != len(buf) {
			t.Fatalf("padding= %d ; Predicted len := %d != real:= %d", labelSize, lenFake, len(buf))
		}
		if !compressionMapsEqual(compressionFake, compressionReal) {
			t.Fatalf("padding= %d ; Fake Compression Map != Real Compression Map\n%s", labelSize, compressionMapsDifference(compressionFake, compressionReal))
		}
	}
}

func TestMsgCompressLengthLargeRecordsWithPaddingPermutation(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("my.service.acme.", TypeSRV)

	for i := 0; i < 250; i++ {
		target := fmt.Sprintf("host-redis-x-%d.test.acme.com.node.dc1.consul.", i)
		msg.Answer = append(msg.Answer, &SRV{Hdr: RR_Header{Name: "redis.service.consul.", Class: 1, Rrtype: TypeSRV, Ttl: 0x3c}, Port: 0x4c57, Target: target})
		msg.Extra = append(msg.Extra, &CNAME{Hdr: RR_Header{Name: target, Class: ClassINET, Rrtype: TypeCNAME, Ttl: 0x3c}, Target: fmt.Sprintf("fx.168.x.%d.", i)})
	}
	for labelSize := 1; labelSize < 63; labelSize++ {
		msg.SetQuestion(fmt.Sprintf("my.%s.service.acme.", strings.Repeat("x", labelSize)), TypeSRV)
		predicted := msg.Len()
		buf, err := msg.Pack()
		if err != nil {
			t.Error(err)
		}
		if predicted != len(buf) {
			t.Fatalf("padding= %d ; predicted compressed length is wrong: predicted %s (len=%d) %d, actual %d", labelSize, msg.Question[0].Name, len(msg.Answer), predicted, len(buf))
		}
	}
}

func TestMsgCompressLengthLargeRecordsAllValues(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("redis.service.consul.", TypeSRV)
	for i := 0; i < 900; i++ {
		target := fmt.Sprintf("host-redis-%d-%d.test.acme.com.node.dc1.consul.", i/256, i%256)
		msg.Answer = append(msg.Answer, &SRV{Hdr: RR_Header{Name: "redis.service.consul.", Class: 1, Rrtype: TypeSRV, Ttl: 0x3c}, Port: 0x4c57, Target: target})
		msg.Extra = append(msg.Extra, &CNAME{Hdr: RR_Header{Name: target, Class: ClassINET, Rrtype: TypeCNAME, Ttl: 0x3c}, Target: fmt.Sprintf("fx.168.%d.%d.", i/256, i%256)})
		predicted := msg.Len()
		buf, err := msg.Pack()
		if err != nil {
			t.Error(err)
		}
		if predicted != len(buf) {
			t.Fatalf("predicted compressed length is wrong for %d records: predicted %s (len=%d) %d, actual %d", i, msg.Question[0].Name, len(msg.Answer), predicted, len(buf))
		}
	}
}

func TestMsgCompressionMultipleQuestions(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("www.example.org.", TypeA)
	msg.Question = append(msg.Question, Question{"other.example.org.", TypeA, ClassINET})

	predicted := msg.Len()
	buf, err := msg.Pack()
	if err != nil {
		t.Error(err)
	}
	if predicted != len(buf) {
		t.Fatalf("predicted compressed length is wrong: predicted %d, actual %d", predicted, len(buf))
	}
}

func TestMsgCompressMultipleCompressedNames(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("www.example.com.", TypeSRV)
	msg.Answer = append(msg.Answer, &MINFO{
		Hdr:   RR_Header{Name: "www.example.com.", Class: 1, Rrtype: TypeSRV, Ttl: 0x3c},
		Rmail: "mail.example.org.",
		Email: "mail.example.org.",
	})
	msg.Answer = append(msg.Answer, &SOA{
		Hdr:  RR_Header{Name: "www.example.com.", Class: 1, Rrtype: TypeSRV, Ttl: 0x3c},
		Ns:   "ns.example.net.",
		Mbox: "mail.example.net.",
	})

	predicted := msg.Len()
	buf, err := msg.Pack()
	if err != nil {
		t.Error(err)
	}
	if predicted != len(buf) {
		t.Fatalf("predicted compressed length is wrong: predicted %d, actual %d", predicted, len(buf))
	}
}

func TestMsgCompressLengthEscapingMatch(t *testing.T) {
	// Although slightly non-optimal, "example.org." and "ex\\097mple.org."
	// are not considered equal in the compression map, even though \097 is
	// a valid escaping of a. This test ensures that the Len code and the
	// Pack code don't disagree on this.

	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("www.example.org.", TypeA)
	msg.Answer = append(msg.Answer, &NS{Hdr: RR_Header{Name: "ex\\097mple.org.", Rrtype: TypeNS, Class: ClassINET}, Ns: "ns.example.org."})

	predicted := msg.Len()
	buf, err := msg.Pack()
	if err != nil {
		t.Error(err)
	}
	if predicted != len(buf) {
		t.Fatalf("predicted compressed length is wrong: predicted %d, actual %d", predicted, len(buf))
	}
}

func TestMsgLengthEscaped(t *testing.T) {
	msg := new(Msg)
	msg.SetQuestion(`\000\001\002.\003\004\005\006\007\008\009.\a\b\c.`, TypeA)

	predicted := msg.Len()
	buf, err := msg.Pack()
	if err != nil {
		t.Error(err)
	}
	if predicted != len(buf) {
		t.Fatalf("predicted compressed length is wrong: predicted %d, actual %d", predicted, len(buf))
	}
}

func TestMsgCompressLengthEscaped(t *testing.T) {
	msg := new(Msg)
	msg.Compress = true
	msg.SetQuestion("www.example.org.", TypeA)
	msg.Answer = append(msg.Answer, &NS{Hdr: RR_Header{Name: `\000\001\002.example.org.`, Rrtype: TypeNS, Class: ClassINET}, Ns: `ns.\e\x\a\m\p\l\e.org.`})
	msg.Answer = append(msg.Answer, &NS{Hdr: RR_Header{Name: `www.\e\x\a\m\p\l\e.org.`, Rrtype: TypeNS, Class: ClassINET}, Ns: "ns.example.org."})

	predicted := msg.Len()
	buf, err := msg.Pack()
	if err != nil {
		t.Error(err)
	}
	if predicted != len(buf) {
		t.Fatalf("predicted compressed length is wrong: predicted %d, actual %d", predicted, len(buf))
	}
}
