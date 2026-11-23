package core_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireSaved(t *testing.T, env *testenv.Env, id, name, content string, revisions int) {
	t.Helper()
	f, ok := env.Emu.Get(id)
	if !ok || f.Trashed || f.Name != name || string(f.Content) != content {
		t.Fatalf("Drive holds %q named %q (there %v, trashed %v), want %q named %q", f.Content, f.Name, ok, f.Trashed,
			content, name)
	}
	if got := len(env.Emu.Revisions(id)); got != revisions {
		t.Fatalf("the file has %d revisions in Drive, want %d", got, revisions)
	}
	for _, f := range env.Emu.All() {
		if f.Trashed {
			t.Fatalf("Drive's trash holds %q", f.Name)
		}
	}
}

func creates(env *testenv.Env) int {
	n := 0
	for _, r := range env.Emu.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/files") {
			n++
		}
	}
	return n
}

func requireRenamedFileTookTheName(t *testing.T, env *testenv.Env, id string, before tried, tmp uint64) {
	t.Helper()
	if got := env.Names(""); !slices.Equal(got, []string{"doc.txt"}) {
		t.Fatalf("root lists %q", got)
	}
	if a := env.MustResolve("doc.txt"); a.Ino != tmp {
		t.Fatalf("doc.txt has the inode %d, want that of the renamed file, %d", a.Ino, tmp)
	}
	if got := readAll(t, env, "doc.txt"); string(got) != "new content" {
		t.Fatalf("doc.txt reads %q", got)
	}
	if it := item(t, env, "doc.txt"); it.ID != id || it.Mime != before.Mime {
		t.Fatalf("doc.txt is the file %s of type %q, want %s of type %q", it.ID, it.Mime, id, before.Mime)
	}
}

func TestSaveByRenameKeepsTheFileID(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	before := item(t, env, "doc.txt")
	env.Emu.ResetRequests()
	writeFile(t, env, "", "doc.tmp", []byte("new content"))
	tmp := env.MustResolve("doc.tmp").Ino
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	requireRenamedFileTookTheName(t, env, id, before, tmp)
	uploadAll(t, env)
	requireSaved(t, env, id, "doc.txt", "new content", 2)
	if n := creates(env); n != 0 {
		t.Fatalf("Drive got %d creations", n)
	}
	if n := len(env.Emu.All()); n != 1 {
		t.Fatalf("Drive holds %d items, want the one file", n)
	}
	env.Restart()
	if got := readAll(t, env, "doc.txt"); string(got) != "new content" {
		t.Fatalf("after a restart doc.txt reads %q", got)
	}
	if it := item(t, env, "doc.txt"); it.ID != id || it.AheadOfDrive() {
		t.Fatalf("after a restart doc.txt is the file %s, changed here: %v", it.ID, it.AheadOfDrive())
	}
}

func TestSaveThroughGIOKeepsTheFileID(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("version 1"))
	env.Start()
	env.Emu.ResetRequests()
	save := func(content string) {
		writeFile(t, env, "", ".goutputstream-A1B2C3", []byte(content))
		rename(t, env, "", ".goutputstream-A1B2C3", "", "doc.txt")
	}
	for n := 2; n <= 4; n++ {
		content := fmt.Sprintf("version %d", n)
		save(content)
		if got := env.Names(""); !slices.Equal(got, []string{"doc.txt"}) {
			t.Fatalf("after save %d the root lists %q", n-1, got)
		}
		uploadAll(t, env)
		requireSaved(t, env, id, "doc.txt", content, n)
	}
	if n := creates(env); n != 0 {
		t.Fatalf("Drive got %d creations", n)
	}

	save("version 5")
	save("version 6")
	uploadAll(t, env)
	requireSaved(t, env, id, "doc.txt", "version 6", 5)
}

func TestSaveByMovingAsideMakesANewFile(t *testing.T) {
	t.Parallel()
	saves := map[string]func(t *testing.T, env *testenv.Env, content []byte){
		"JetBrains": func(t *testing.T, env *testenv.Env, content []byte) {
			writeFile(t, env, "", "doc.txt___jb_tmp___", content)
			rename(t, env, "", "doc.txt", "", "doc.txt___jb_old___")
			rename(t, env, "", "doc.txt___jb_tmp___", "", "doc.txt")
			if err := env.FS.Unlink(env.MustResolve("").Ino, "doc.txt___jb_old___"); err != nil {
				t.Fatal(err)
			}
		},
		"vim": func(t *testing.T, env *testenv.Env, content []byte) {
			rename(t, env, "", "doc.txt", "", "doc.txt~")
			writeFile(t, env, "", "doc.txt", content)
			if err := env.FS.Unlink(env.MustResolve("").Ino, "doc.txt~"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for editor, save := range saves {
		t.Run(editor, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("version 1"))
			env.Start()
			save(t, env, []byte("version 2"))
			if got := env.Names(""); !slices.Equal(got, []string{"doc.txt"}) {
				t.Fatalf("after the save the root lists %q", got)
			}
			uploadAll(t, env)
			if f, _ := env.Emu.Get(id); !f.Trashed || string(f.Content) != "version 1" {
				t.Fatalf("the file moved aside: trashed %v, content %q", f.Trashed, f.Content)
			}
			if f := inDrive(t, env, "doc.txt"); f.ID == id || string(f.Content) != "version 2" ||
				len(env.Emu.Revisions(f.ID)) != 1 {
				t.Fatalf("doc.txt in Drive is the file %s with %q", f.ID, f.Content)
			}
		})
	}
}

func TestSaveOverAFileChangedInDriveIsAConflict(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	writeFile(t, env, "", "doc.tmp", []byte("mine"))
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); string(f.Content) != "theirs" || len(env.Emu.Revisions(id)) != 2 {
		t.Fatalf("Drive holds %q in %d revisions, want its own change alone", f.Content, len(env.Emu.Revisions(id)))
	}
	poll(t, env)
	if got := readAll(t, env, "doc.txt"); string(got) != "theirs" {
		t.Fatalf("doc.txt reads %q, want Drive's version", got)
	}
	requireShelved(t, env, "", "doc", ".txt", []byte("mine"))
}

func TestSaveOverAFileChangedHere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	fh := openWriter(t, env, "doc.txt", 0)
	write(t, env, fh, 0, []byte("edited here"))
	env.FS.Release(fh)
	writeFile(t, env, "", "doc.tmp", []byte("saved"))
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	if got := readAll(t, env, "doc.txt"); string(got) != "saved" {
		t.Fatalf("doc.txt reads %q", got)
	}
	env.Restart()
	if got := readAll(t, env, "doc.txt"); string(got) != "saved" {
		t.Fatalf("after a restart doc.txt reads %q", got)
	}
	uploadAll(t, env)
	requireSaved(t, env, id, "doc.txt", "saved", 2)
	if n, err := env.FS.Store().DirtyBytes(); err != nil || n != 0 {
		t.Fatalf("%d bytes wait for Drive after the upload: %v", n, err)
	}
}

func TestSavedContentDriveNeverTakesGoesToLostAndFound(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	writeFile(t, env, "", "doc.tmp", []byte("new"))
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	env.Emu.SetUploadFault(func(string) int { return http.StatusForbidden })
	rounds(env, 6)
	if got := readAll(t, env, "doc.txt"); string(got) != "old" {
		t.Fatalf("doc.txt reads %q, want Drive's version", got)
	}
	if it := item(t, env, "doc.txt"); it.ID != id {
		t.Fatalf("doc.txt is the file %s, want %s", it.ID, id)
	}
	requireShelved(t, env, "", "doc", ".txt", []byte("new"))
	if f, _ := env.Emu.Get(id); string(f.Content) != "old" || f.Trashed {
		t.Fatalf("Drive holds %q, trashed %v", f.Content, f.Trashed)
	}
}
