package core_test

import (
	"bytes"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireFolderGoneShelves(t *testing.T, remove func(env *testenv.Env, id string)) {
	t.Helper()
	env := testenv.New(t)
	top := env.Emu.Mkdir(env.Emu.Root(), "Проект")
	drafts := env.Emu.Mkdir(top, "черновики")
	env.Emu.WriteFile(drafts, "clean.txt", []byte("untouched"))
	old := testenv.RandomBytes(20000, 420)
	env.Emu.WriteFile(drafts, "edited.txt", old)
	env.Start()
	fh := openWriter(t, env, "Проект/черновики/edited.txt", 0)
	write(t, env, fh, 4096, testenv.RandomBytes(4096, 421))
	env.FS.Release(fh)
	edited := append([]byte(nil), old...)
	copy(edited[4096:], testenv.RandomBytes(4096, 421))
	readAll(t, env, "Проект/черновики/edited.txt")
	fresh := testenv.RandomBytes(3000, 422)
	writeFile(t, env, "Проект/черновики", "new.txt", fresh)
	mkdir(t, env, "Проект/черновики", "deep")
	deep := testenv.RandomBytes(100, 423)
	writeFile(t, env, "Проект/черновики/deep", "note.txt", deep)
	env.Emu.ResetRequests()
	remove(env, top)
	poll(t, env)
	if names := env.Names(""); len(names) != 0 || !hasLost(env) {
		t.Fatalf("the root holds %q, want Lost and Found alone", names)
	}
	requireShelved(t, env, "Проект/черновики/deep", "note", ".txt", deep)
	requireFolderShelvedByName(t, env, edited, fresh)
	if n := lostFiles(t, env); n != 3 {
		t.Fatalf("status counts %d files in Lost and Found", n)
	}
	uploadAll(t, env)
	if n := writesToDrive(env); n != 0 {
		t.Fatalf("%d requests changed Drive", n)
	}
}

func requireFolderShelvedByName(t *testing.T, env *testenv.Env, edited, fresh []byte) {
	t.Helper()
	names := lostNames(env, lostDir+"/Проект/черновики")
	if len(names) != 3 {
		t.Fatalf("Lost and Found holds %q of the deleted folder, want the two files not uploaded and the folder of the "+
			"third", names)
	}
	for _, n := range names {
		p := lostDir + "/Проект/черновики/" + n
		switch {
		case conflictName("edited", ".txt").MatchString(n):
			if !bytes.Equal(readAll(t, env, p), edited) {
				t.Fatal("the edited file in Lost and Found is not the local version")
			}
		case conflictName("new", ".txt").MatchString(n):
			if !bytes.Equal(readAll(t, env, p), fresh) {
				t.Fatal("the new file in Lost and Found is not the local version")
			}
		case n != "deep":
			t.Fatalf("Lost and Found holds %q", n)
		}
	}
}

func TestFolderDeletedInDriveShelvesWhatWasNotUploaded(t *testing.T) {
	t.Parallel()
	gone := map[string]func(env *testenv.Env, id string){
		"trashed": func(env *testenv.Env, id string) {
			env.Emu.Update(id, func(f *emulator.File) { f.Trashed = true })
		},
		"deleted": func(env *testenv.Env, id string) { env.Emu.Delete(id) },
	}
	for name, remove := range gone {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireFolderGoneShelves(t, remove)
		})
	}
}

func TestWriteIntoAFileDriveTrashedGoesToLostAndFound(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(8192, 430)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.txt", content)
	env.Start()
	fh := openWriter(t, env, "f.txt", 0)
	readAll(t, env, "f.txt")
	env.Emu.Update(id, func(f *emulator.File) { f.Trashed = true })
	poll(t, env)
	patch := testenv.RandomBytes(4096, 431)
	write(t, env, fh, 0, patch)
	env.FS.Release(fh)
	copy(content, patch)
	uploadAll(t, env)
	requireShelved(t, env, "", "f", ".txt", content)
	if f, _ := env.Emu.Get(id); !f.Trashed || bytes.Equal(f.Content, content) {
		t.Fatal("the write went to the file in Drive's trash, or took it out")
	}
}
