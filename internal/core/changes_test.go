package core_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestChangeDuringInitialLoadIsNotLost(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	first := emu.WriteFile(emu.Root(), "a-first.txt", nil)
	for i := range 1500 {
		emu.WriteFile(emu.Root(), fmt.Sprintf("f%04d", i), nil)
	}

	emu.OnListPage(func(n int) {
		if n == 1 {
			emu.Update(first, func(f *emulator.File) { f.Name = "renamed.txt" })
		}
	})
	env.Start()
	names := env.Names("")
	if !slices.Contains(names, "renamed.txt") || slices.Contains(names, "a-first.txt") {
		t.Fatalf("rename during the initial load is lost: %d names, renamed=%v first=%v",
			len(names), slices.Contains(names, "renamed.txt"), slices.Contains(names, "a-first.txt"))
	}
}

func TestChangesInDriveReachTheMount(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	root := emu.Root()
	docs := emu.Mkdir(root, "Docs")
	other := emu.Mkdir(root, "Other")
	edited := emu.WriteFile(docs, "edited.txt", []byte("old content"))
	renamed := emu.WriteFile(docs, "old-name.txt", []byte("r"))
	moved := emu.WriteFile(docs, "moved.txt", []byte("m"))
	trashed := emu.WriteFile(docs, "trashed.txt", []byte("t"))
	restored := emu.Put(emulator.File{Name: "restored.txt", MimeType: "text/plain", Parents: []string{docs}, Trashed: true,
		Content: []byte("back")})
	deleted := emu.WriteFile(docs, "deleted.txt", []byte("d"))
	env.Start()
	if got := readAll(t, env, "Docs/edited.txt"); string(got) != "old content" {
		t.Fatalf("edited.txt before the change: %q", got)
	}

	emu.WriteFile(docs, "new.txt", []byte("n"))
	emu.Update(edited, func(f *emulator.File) { f.Content = []byte("new, longer content") })
	emu.Update(renamed, func(f *emulator.File) { f.Name = "new-name.txt" })
	emu.Update(moved, func(f *emulator.File) { f.Parents = []string{other} })
	emu.Update(trashed, func(f *emulator.File) { f.Trashed = true })
	emu.Update(restored, func(f *emulator.File) { f.Trashed = false })
	emu.Delete(deleted)
	poll(t, env)

	want := []string{"edited.txt", "new-name.txt", "new.txt", "restored.txt"}
	if got := env.Names("Docs"); !slices.Equal(got, want) {
		t.Fatalf("Docs lists %q, want %q", got, want)
	}
	if got := env.Names("Other"); !slices.Equal(got, []string{"moved.txt"}) {
		t.Fatalf("Other lists %q", got)
	}
	if got := readAll(t, env, "Docs/edited.txt"); string(got) != "new, longer content" {
		t.Fatalf("edited.txt after the change reads %q", got)
	}
}

func TestChangesTokenSurvivesRestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	f := emu.WriteFile(emu.Root(), "f.txt", []byte("1"))
	env.Start()

	emu.Update(f, func(f *emulator.File) { f.Name = "while-down.txt" })
	env.Restart()
	emu.ResetRequests()
	poll(t, env)
	if got := env.Names(""); !slices.Equal(got, []string{"while-down.txt"}) {
		t.Fatalf("root lists %q after catching up", got)
	}
	for _, r := range emu.Requests() {
		if r.Path == "/drive/v3/files" {
			t.Fatalf("catching up listed Drive again: %+v", r)
		}
	}
}

func TestChangesAskOnlyForMyDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.ResetRequests()
	poll(t, env)
	for _, r := range env.Emu.Requests() {
		if r.Path == "/drive/v3/changes" && r.Query.Get("restrictToMyDrive") != "true" {
			t.Fatalf("changes asked beyond My Drive: %v", r.Query)
		}
	}
}

func TestChangesOfMoreThanOnePageAreAllApplied(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	const files = 1100
	for i := range files {
		env.Emu.WriteFile(env.Emu.Root(), fmt.Sprintf("f%04d", i), nil)
	}
	env.Emu.ResetRequests()
	poll(t, env)
	if n := changesPages(env); n < 2 {
		t.Fatalf("scenario not built: %d changes came in %d pages", files, n)
	}
	if got := len(env.Names("")); got != files {
		t.Fatalf("after the poll the root lists %d files, want %d", got, files)
	}
	token, _, err := env.FS.Store().GetState(meta.StateChangesToken)
	if err != nil || token == "" {
		t.Fatalf("the token after the pages is %q: %v", token, err)
	}
	env.Emu.ResetRequests()
	poll(t, env)
	if n := changesPages(env); n != 1 {
		t.Fatalf("the poll after the pages asked %d pages, want one that brings nothing", n)
	}
	if again, _, _ := env.FS.Store().GetState(meta.StateChangesToken); again != token {
		t.Fatalf("a poll that brought nothing moved the token from %q to %q", token, again)
	}
	if got := len(env.Names("")); got != files {
		t.Fatalf("after the second poll the root lists %d files", got)
	}
}

func TestPollThatFailsBeforeTheDatabaseIsRepeated(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	var failing atomic.Bool
	fault := errors.New("the poll is cut short")
	env.Tune = func(o *core.Options) {
		o.PollFault = func() error {
			if failing.Load() {
				return fault
			}
			return nil
		}
	}
	id := env.Emu.WriteFile(env.Emu.Root(), "f.txt", []byte("the first version"))
	env.Start()
	readClosed(t, env, "f.txt")
	token, _, err := env.FS.Store().GetState(meta.StateChangesToken)
	if err != nil {
		t.Fatal(err)
	}
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("the second version, longer") })
	failing.Store(true)
	if err := env.FS.PollChanges(context.Background()); !errors.Is(err, fault) {
		t.Fatalf("the poll got %v, want the fault", err)
	}
	if now, _, _ := env.FS.Store().GetState(meta.StateChangesToken); now != token {
		t.Fatalf("the poll that failed moved the token from %q to %q", token, now)
	}
	if it := item(t, env, "f.txt"); it.Size != int64(len("the first version")) {
		t.Fatalf("the poll that failed left the file with %d bytes in the database", it.Size)
	}
	failing.Store(false)
	poll(t, env)
	if got := readAll(t, env, "f.txt"); string(got) != "the second version, longer" {
		t.Fatalf("after the poll went again the file reads %q", got)
	}
	requireNoLost(t, env)
}

func TestFileThatLeavesMyDriveLeavesTheMount(t *testing.T) {
	t.Parallel()
	for name, how := range map[string]emulator.Parentless{
		"removed": emulator.ParentlessRemoved, "without parents": emulator.ParentlessShown,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", []byte("shared"))
			env.Start()
			env.Emu.TellParentless(how)
			env.Emu.Update(id, func(f *emulator.File) { f.Parents = nil })
			poll(t, env)
			if _, err := env.Resolve("f.bin"); err == nil {
				t.Fatal("a file that left My Drive is still in the mount")
			}
		})
	}
}

func changesPages(env *testenv.Env) (n int) {
	for _, r := range env.Emu.Requests() {
		if r.Path == "/drive/v3/changes" {
			n++
		}
	}
	return n
}
