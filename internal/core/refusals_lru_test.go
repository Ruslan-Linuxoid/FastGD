package core

import (
	"strconv"
	"testing"
	"time"
)

func TestRefusalsKeepTheLatestPairs(t *testing.T) {
	t.Parallel()
	r := newRefusals()
	now := time.Unix(0, 0)
	for i := range refusalsKept {
		r.tell(refusal{id: strconv.Itoa(i), right: "canDownload"}, now)
	}
	if tell, _ := r.tell(refusal{id: "0", right: "canDownload"}, now); tell {
		t.Fatal("a repeat of a kept pair takes a line")
	}
	r.tell(refusal{id: "new", right: "canDownload"}, now)
	if tell, _ := r.tell(refusal{id: "0", right: "canDownload"}, now); tell {
		t.Fatal("the pair refused last went, not the one refused longest ago")
	}
	if tell, n := r.tell(refusal{id: "1", right: "canDownload"}, now); !tell || n != 0 {
		t.Fatalf("the pair that went tells %v with %d repeats, want a line with none", tell, n)
	}
	if tell, _ := r.tell(refusal{id: "0", right: "canModifyContent"}, now); !tell {
		t.Fatal("another right on the same item takes no line")
	}
	if tell, _ := r.tell(refusal{id: "0", right: "canDownload", what: "cut"}, now); !tell {
		t.Fatal("the same right asked for another thing takes no line")
	}
}
