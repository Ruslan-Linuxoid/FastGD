package meta

import "testing"

func TestQuotaOfAnOlderBuildHasNoFiles(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	if err := s.SetState(StateQuota, "100 40"); err != nil {
		t.Fatal(err)
	}
	limit, usage, _, filesKnown, ok := s.Quota()
	if !ok || limit != 100 || usage != 40 || filesKnown {
		t.Fatalf("the old quota reads as %d %d, files known %v, ok %v", limit, usage, filesKnown, ok)
	}
	if err := s.SetQuota(100, 40, 30); err != nil {
		t.Fatal(err)
	}
	if _, _, files, filesKnown, ok := s.Quota(); !ok || !filesKnown || files != 30 {
		t.Fatalf("the quota reads files %d, known %v, ok %v", files, filesKnown, ok)
	}
}
