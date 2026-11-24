package core_test

import (
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestTheEntryOfASecondNameIsNotKept(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	project := env.Emu.Put(emulator.File{Name: "Project", MimeType: emulator.MimeFolder, SharedWithMe: true})
	env.Emu.Put(emulator.File{Name: "plan.txt", Parents: []string{project}, Content: []byte("p"), SharedWithMe: true})
	env.Start()
	want := map[string]bool{"Project": false, "plan.txt": true}
	shared := env.MustResolve("/Shared with me")
	for name, uncached := range want {
		a, err := env.FS.Lookup(shared.Ino, name)
		if err != nil || a.NameUncached != uncached {
			t.Errorf("the lookup of %s keeps no entry: %v, %v; want %v", name, a.NameUncached, err, uncached)
		}
	}
	entries, err := env.FS.ReadDir(shared.Ino)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Attr.NameUncached != want[e.Name] {
			t.Errorf("the listing of %s keeps no entry: %v, want %v", e.Name, e.Attr.NameUncached, want[e.Name])
		}
	}
	if a := env.MustResolve("/Shared with me/Project/plan.txt"); a.NameUncached {
		t.Error("the item keeps no entry in its own folder")
	}
}
