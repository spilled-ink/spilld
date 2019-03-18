package dns

import (
	"testing"
)

func TestDynamicUpdateUnpack(t *testing.T) {
	// From https://github.com/miekg/dns/issues/150#issuecomment-62296803
	// It should be an update message for the zone "example.",
	// deleting the A RRset "example." and then adding an A record at "example.".
	// class ANY, TYPE A
	buf := []byte{171, 68, 40, 0, 0, 1, 0, 0, 0, 2, 0, 0, 7, 101, 120, 97, 109, 112, 108, 101, 0, 0, 6, 0, 1, 192, 12, 0, 1, 0, 255, 0, 0, 0, 0, 0, 0, 192, 12, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 127, 0, 0, 1}
	msg := new(Msg)
	err := msg.Unpack(buf)
	if err != nil {
		t.Errorf("failed to unpack: %v\n%s", err, msg.String())
	}
}

func TestDynamicUpdateZeroRdataUnpack(t *testing.T) {
	m := new(Msg)
	rr := &RR_Header{Name: ".", Rrtype: 0, Class: 1, Ttl: ^uint32(0), Rdlength: 0}
	m.Answer = []RR{rr, rr, rr, rr, rr}
	m.Ns = m.Answer
	for n, s := range TypeToString {
		rr.Rrtype = n
		bytes, err := m.Pack()
		if err != nil {
			t.Errorf("failed to pack %s: %v", s, err)
			continue
		}
		if err := new(Msg).Unpack(bytes); err != nil {
			t.Errorf("failed to unpack %s: %v", s, err)
		}
	}
}
