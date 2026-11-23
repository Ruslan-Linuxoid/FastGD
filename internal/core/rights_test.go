package core_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func foreign(env *testenv.Env, name string, cut func(*emulator.File)) string {
	f := emulator.File{Name: name, MimeType: "text/plain", Parents: []string{env.Emu.Root()}, Content: []byte("theirs")}
	cut(&f)
	return env.Emu.Put(f)
}

func writesToDrive(env *testenv.Env) int {
	n := 0
	for _, r := range env.Emu.Requests() {
		if r.Method != http.MethodGet {
			n++
		}
	}
	return n
}

func TestFileWithoutModifyRightIsReadOnly(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	foreign(env, "theirs.txt", func(f *emulator.File) { f.CannotModify = true })
	env.Start()
	env.Emu.ResetRequests()
	a := env.MustResolve("theirs.txt")
	if a.Mode&0o777 != 0o444 {
		t.Fatalf("a file the user may not change has mode %o", a.Mode&0o777)
	}
	for _, flags := range []uint32{syscall.O_WRONLY, syscall.O_RDWR, syscall.O_RDONLY | syscall.O_TRUNC} {
		if _, err := env.FS.Open(context.Background(), a.Ino, flags); err != syscall.EACCES {
			t.Fatalf("open with %#x got %v, want EACCES", flags, err)
		}
	}
	if err := env.FS.Truncate(context.Background(), a.Ino, 0); err != syscall.EACCES {
		t.Fatalf("truncate got %v, want EACCES", err)
	}
	if got := readAll(t, env, "theirs.txt"); string(got) != "theirs" {
		t.Fatalf("the file reads %q", got)
	}
	uploadAll(t, env)
	if n := writesToDrive(env); n != 0 {
		t.Fatalf("%d requests went to Drive", n)
	}
}

func TestItemWithoutTrashRightStays(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	foreign(env, "kept.txt", func(f *emulator.File) { f.CannotTrash = true })
	env.Emu.Put(emulator.File{Name: "kept", MimeType: emulator.MimeFolder, Parents: []string{env.Emu.Root()},
		CannotTrash: true})
	env.Start()
	env.Emu.ResetRequests()
	root := env.MustResolve("").Ino
	if err := env.FS.Unlink(root, "kept.txt"); err != syscall.EACCES {
		t.Fatalf("unlink got %v, want EACCES", err)
	}
	if err := env.FS.Rmdir(root, "kept"); err != syscall.EACCES {
		t.Fatalf("rmdir got %v, want EACCES", err)
	}
	if got := env.Names(""); !slices.Contains(got, "kept.txt") || !slices.Contains(got, "kept") {
		t.Fatalf("the root lists %q", got)
	}
	uploadAll(t, env)
	if n := writesToDrive(env); n != 0 {
		t.Fatalf("%d requests went to Drive", n)
	}
}

func TestRenameAndMoveFollowTheRights(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	foreign(env, "named.txt", func(f *emulator.File) { f.CannotRename = true })
	foreign(env, "fixed.txt", func(f *emulator.File) { f.CannotMove = true })
	env.Emu.Put(emulator.File{Name: "closed", MimeType: emulator.MimeFolder, Parents: []string{env.Emu.Root()},
		CannotAddChildren: true})
	env.Start()
	mkdir(t, env, "", "open")
	writeFile(t, env, "", "mine.txt", []byte("mine"))
	uploadAll(t, env)
	env.Emu.ResetRequests()
	root := env.MustResolve("").Ino
	open, closed := env.MustResolve("open").Ino, env.MustResolve("closed").Ino
	for _, c := range []struct {
		name     string
		from, to uint64
		old, new string
	}{
		{"rename without the right", root, root, "named.txt", "other.txt"},
		{"move without the right", root, open, "fixed.txt", "fixed.txt"},
		{"move into a closed folder", root, closed, "mine.txt", "mine.txt"},
	} {
		if err := env.FS.Rename(c.from, c.old, c.to, c.new, 0); err != syscall.EACCES {
			t.Fatalf("%s got %v, want EACCES", c.name, err)
		}
	}

	rename(t, env, "", "named.txt", "open", "named.txt")
	if _, _, err := env.FS.Create(closed, "new.txt", syscall.O_WRONLY); err != syscall.EACCES {
		t.Fatalf("a create in a closed folder got %v, want EACCES", err)
	}
	if _, err := env.FS.Mkdir(closed, "d"); err != syscall.EACCES {
		t.Fatalf("a mkdir in a closed folder got %v, want EACCES", err)
	}
	uploadAll(t, env)
	moved := false
	for _, r := range env.Emu.Requests() {
		if r.Method != http.MethodPatch {
			continue
		}
		if !strings.Contains(r.Query.Get("addParents"), item(t, env, "open").ID) {
			t.Fatalf("a refused change went to Drive: %s %s %v", r.Method, r.Path, r.Query)
		}
		moved = true
	}
	if !moved {
		t.Fatal("the move the rights allow did not go to Drive")
	}
}

func TestChangedRightsShowAfterAPoll(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "shared.txt", []byte("x"))
	env.Start()
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	if a := env.MustResolve("shared.txt"); a.Mode&0o777 != 0o644 {
		t.Fatalf("mode %o before the change", a.Mode&0o777)
	}
	env.Emu.Update(id, func(f *emulator.File) { f.CannotModify = true })
	poll(t, env)
	a := env.MustResolve("shared.txt")
	if a.Mode&0o777 != 0o444 {
		t.Fatalf("after the owner took the right the mode is %o", a.Mode&0o777)
	}
	waitUntil(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return slices.Contains(rec.inodes, fmt.Sprintf("%d content=false", a.Ino))
	})
	env.Emu.Update(id, func(f *emulator.File) { f.CannotModify = false })
	poll(t, env)
	if a := env.MustResolve("shared.txt"); a.Mode&0o777 != 0o644 {
		t.Fatalf("after the owner gave the right back the mode is %o", a.Mode&0o777)
	}
}

func TestRightsRefuseTimeReplaceAndMoveOut(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	closed := env.Emu.Put(emulator.File{Name: "closed", MimeType: emulator.MimeFolder, Parents: []string{root},
		CannotRemoveChildren: true})
	env.Emu.Put(emulator.File{Name: "in.txt", MimeType: "text/plain", Parents: []string{closed}, Content: []byte("in")})
	foreign(env, "theirs.txt", func(f *emulator.File) { f.CannotModify, f.CannotTrash = true, true })
	env.Start()
	writeFile(t, env, "", "mine.txt", []byte("mine"))
	if err := env.FS.SetMtime(env.MustResolve("theirs.txt").Ino, time.Now()); err != syscall.EACCES {
		t.Fatalf("utimes on a file the user may not change got %v, want EACCES", err)
	}
	if err := env.FS.Rename(env.MustResolve("").Ino, "mine.txt", env.MustResolve("").Ino, "theirs.txt",
		0); err != syscall.EACCES {
		t.Fatalf("a rename over a file the user may not trash got %v, want EACCES", err)
	}
	if err := env.FS.Rename(env.MustResolve("closed").Ino, "in.txt", env.MustResolve("").Ino, "in.txt",
		0); err != syscall.EACCES {
		t.Fatalf("a move out of a folder that keeps its items got %v, want EACCES", err)
	}
}

func TestRightLeftOutByDriveIsGiven(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	silent := env.Emu.Put(emulator.File{Name: "silent", MimeType: emulator.MimeFolder, Parents: []string{root},
		CannotRemoveChildren: true, RightsLeftOut: []string{"canRemoveChildren"}})
	env.Emu.Put(emulator.File{Name: "a.txt", MimeType: "text/plain", Parents: []string{silent}, Content: []byte("a")})
	closed := env.Emu.Put(emulator.File{Name: "closed", MimeType: emulator.MimeFolder, Parents: []string{root},
		CannotRemoveChildren: true})
	env.Emu.Put(emulator.File{Name: "b.txt", MimeType: "text/plain", Parents: []string{closed}, Content: []byte("b")})
	foreign(env, "free.txt", func(f *emulator.File) {
		f.CannotModify, f.CannotTrash, f.CannotRename = true, true, true
		f.RightsLeftOut = []string{"canModifyContent", "canTrash", "canRename"}
	})
	env.Start()
	rootIno := env.MustResolve("").Ino
	if err := env.FS.Rename(env.MustResolve("silent").Ino, "a.txt", rootIno, "a.txt", 0); err != nil {
		t.Fatalf("a move out of a folder whose right Drive left out got %v", err)
	}
	testenv.RequireErrno(t, env.FS.Rename(env.MustResolve("closed").Ino, "b.txt", rootIno, "b.txt", 0), syscall.EACCES)
	if a := env.MustResolve("free.txt"); a.Mode != syscall.S_IFREG|0o644 {
		t.Fatalf("a file whose right to change Drive left out shows the mode %o", a.Mode)
	}
	if err := env.FS.Rename(rootIno, "free.txt", rootIno, "renamed.txt", 0); err != nil {
		t.Fatalf("a rename whose right Drive left out got %v", err)
	}
	if err := env.FS.Unlink(rootIno, "renamed.txt"); err != nil {
		t.Fatalf("a removal whose right Drive left out got %v", err)
	}
}

func TestMoveGoesByTheRightsTheClientReads(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	to := env.Emu.Mkdir(root, "to")
	foreign(env, "unread.txt", func(f *emulator.File) { f.CannotMoveWithinDrive = true })
	foreign(env, "fixed.txt", func(f *emulator.File) { f.CannotMove = true })
	target := env.Emu.WriteFile(root, "target.txt", []byte("t"))
	link := env.Emu.Put(emulator.File{Name: "link", MimeType: emulator.MimeShortcut, Parents: []string{root},
		ShortcutTarget: target})
	env.Start()
	rootIno, toIno := env.MustResolve("").Ino, env.MustResolve("to").Ino
	if err := env.FS.Rename(rootIno, "unread.txt", toIno, "unread.txt", 0); err != nil {
		t.Fatalf("a move of an item without canMoveItemWithinDrive got %v, want it to go through", err)
	}
	testenv.RequireErrno(t, env.FS.Rename(rootIno, "fixed.txt", toIno, "fixed.txt", 0), syscall.EACCES)
	testenv.RequireErrno(t, env.FS.Rename(rootIno, "link", toIno, "link", 0), syscall.EACCES)

	rename(t, env, "", "link", "", "renamed")
	uploadAll(t, env)
	if f, _ := env.Emu.Get(link); f.Name != "renamed" || f.Parents[0] != root {
		t.Fatalf("the shortcut is %q under %v in Drive", f.Name, f.Parents)
	}
	if f := inDrive(t, env, "to", "unread.txt"); len(f.Parents) != 1 || f.Parents[0] != to {
		t.Fatalf("the moved item lies under %v in Drive", f.Parents)
	}
}
