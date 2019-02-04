package imapparser

import "testing"

var seqContainsTests = []struct {
	seqs    []SeqRange
	want    []uint32
	wantNot []uint32
}{
	{
		seqs: []SeqRange{SeqRange{0, 0}},
		want: []uint32{1, 2, 3, 4},
	},
	{
		seqs:    []SeqRange{SeqRange{1, 1}, SeqRange{3, 4}},
		want:    []uint32{1, 3, 4},
		wantNot: []uint32{2, 5},
	},
	{
		seqs:    []SeqRange{SeqRange{4, 0}},
		want:    []uint32{4, 5, 6},
		wantNot: []uint32{1, 2, 3},
	},
}

func TestSeqContains(t *testing.T) {
	for _, test := range seqContainsTests {
		for _, id := range test.want {
			if !SeqContains(test.seqs, id) {
				t.Errorf("SeqContains(%v, %d)=false, want true", test.seqs, id)
			}
		}
		for _, id := range test.wantNot {
			if SeqContains(test.seqs, id) {
				t.Errorf("SeqContains(%v, %d)=true, want false", test.seqs, id)
			}
		}
	}
}
