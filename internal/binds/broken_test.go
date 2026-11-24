package binds_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestABrokenListBindsNothingAndSaysSo(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	file := filepath.Join(s.dir, "mounts.json")
	if err := os.WriteFile(file, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &fakeBinder{}
	m := s.manager(t, true, b)
	m.Tick()
	_, addErr := m.Add("My Drive/Work", s.place("work"))
	_, listErr := m.List()
	removeErr := m.Remove(s.place("work"))
	for name, err := range map[string]error{"add": addErr, "list": listErr, "remove": removeErr} {
		if err == nil || !strings.Contains(err.Error(), file+" cannot be read") ||
			!strings.Contains(err.Error(), "delete the file") {
			t.Fatalf("%s answered %v", name, err)
		}
	}
	if len(b.bound) != 0 || len(b.unbound) != 0 {
		t.Fatalf("the helper was asked %q and %q", b.bound, b.unbound)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "{not json" {
		t.Fatalf("the list reads %q, %v", data, err)
	}
}
