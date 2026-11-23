package drive

import (
	"encoding/json"
	"testing"
)

type rightsAnswer struct {
	Files []struct {
		ID           string
		Size         string
		Capabilities map[string]bool
	}
	NextPageToken string
}

func TestRightLeftOutOfAnAnswerIsGiven(t *testing.T) {
	t.Parallel()
	answer := []byte(`{"files":[{"id":"a","size":"12345678901234567890","capabilities":{"canTrash":false,` +
		`"canRename":true}},{"id":"b"}],"nextPageToken":"t"}`)
	var got rightsAnswer

	if err := json.Unmarshal(fillRights(answer), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || got.NextPageToken != "t" || got.Files[0].Size != "12345678901234567890" {
		t.Fatalf("the answer came out as %+v", got)
	}
	requireRightsFilled(t, got.Files[0].Capabilities)
	if got.Files[1].Capabilities != nil {
		t.Fatalf("an item without capabilities got %v", got.Files[1].Capabilities)
	}
	requireAnswersWithoutRightsUntouched(t)
}

func requireRightsFilled(t *testing.T, caps map[string]bool) {
	t.Helper()
	if caps["canTrash"] || !caps["canRename"] {
		t.Fatalf("the rights that came are %v, want them as they came", caps)
	}
	for _, right := range rightFields() {
		if right != "canTrash" && !caps[right] {
			t.Fatalf("the right %s, left out of the answer, reads as denied", right)
		}
	}
}

func requireAnswersWithoutRightsUntouched(t *testing.T) {
	t.Helper()
	for _, plain := range []string{`{"id":"a"}`, `not json with "capabilities"`, ``} {
		if out := fillRights([]byte(plain)); string(out) != plain {
			t.Fatalf("the answer %q came out as %q", plain, out)
		}
	}
}
