package core_test

import (
	"context"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestLinkFileNamesTellTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	env.Emu.WriteGoogleFile(root, "A", mimeDoc)
	env.Clock.Advance(time.Second)
	env.Emu.WriteGoogleFile(root, "A", mimeDoc)
	env.Emu.WriteGoogleFile(root, "Plain", mimeDoc)
	env.Start()
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	ino := env.MustResolve("").Ino
	if err := env.FS.Unlink(ino, "A.gdoc"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return heard(rec, ino, "A.gdoc") })
	if got := env.Names(""); !slices.Equal(got, []string{"A (1).gdoc", "Plain.gdoc"}) {
		t.Fatalf("after the removal of a document the root lists %q", got)
	}

	rename(t, env, "", "Plain.gdoc", "", "Notes")
	if got := env.Names(""); !slices.Contains(got, "Notes.gdoc") {
		t.Fatalf("the root lists %q", got)
	}
	waitUntil(t, func() bool { return heard(rec, ino, "Notes") })
}

func TestGoogleTypeWithoutAnExtensionIsALinkFile(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	const vids = mimeGoogle + "vid"
	id := env.Emu.WriteGoogleFile(env.Emu.Root(), "Clip", vids)
	other := env.Emu.WriteGoogleFile(env.Emu.Root(), "Chart", mimeGoogle+"drive-sdk.12345")
	env.Start()
	if got := env.Names(""); !slices.Equal(got, []string{"Chart.glink", "Clip.glink"}) {
		t.Fatalf("the root lists %q", got)
	}
	requireLinkFileReadOnly(t, env, env.MustResolve("Clip.glink"), emulator.ViewLink(vids, id))

	env.Emu.ResetRequests()
	if page := readLinkPage(t, env, env.MustResolve("Clip.glink").Ino); !strings.Contains(string(page),
		`url=https://drive.google.com/open?id=`+id+`"`) || len(env.Emu.Requests()) != 0 {
		t.Fatalf("the page of a type with no fixed address is %q, after %d requests", page, len(env.Emu.Requests()))
	}
	if it := item(t, env, "Chart.glink"); it.ID != other {
		t.Fatalf("the file of another application shows as %q", it.ID)
	}
	requireLinkFileMovedAndTrashed(t, env, id)
}

func requireLinkFileReadOnly(t *testing.T, env *testenv.Env, a core.Attr, address string) {
	t.Helper()
	page := readLinkPage(t, env, a.Ino)
	if a.Mode != syscall.S_IFREG|0o444 || a.Size != int64(len(page)) {
		t.Fatalf("mode %o, size %d, want a read-only file of %d", a.Mode, a.Size, len(page))
	}
	for _, flags := range []uint32{syscall.O_WRONLY, syscall.O_RDWR} {
		if _, err := env.FS.Open(context.Background(), a.Ino, flags); err != syscall.EACCES {
			t.Fatalf("open with %#x got %v, want EACCES", flags, err)
		}
	}
	if err := env.FS.Truncate(context.Background(), a.Ino, 0); err != syscall.EACCES {
		t.Fatalf("truncate got %v, want EACCES", err)
	}
	if got, err := env.FS.GetXattr(t.Context(), a.Ino, "user.drive.item_open_url"); err != nil ||
		got != address {
		t.Fatalf("the address reads %q, %v", got, err)
	}
}

func requireLinkFileMovedAndTrashed(t *testing.T, env *testenv.Env, id string) {
	t.Helper()
	mkdir(t, env, "", "dir")
	rename(t, env, "", "Clip.glink", "dir", "Film.GLink")
	if got := env.Names("dir"); !slices.Equal(got, []string{"Film.glink"}) {
		t.Fatalf("dir lists %q", got)
	}
	uploadAll(t, env)
	if f, ok := env.Emu.Get(id); !ok || f.Name != "Film" || f.Parents[0] != item(t, env, "dir").ID {
		t.Fatalf("Drive has the item as %q under %v", f.Name, f.Parents)
	}
	if err := env.FS.Unlink(env.MustResolve("dir").Ino, "Film.glink"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed {
		t.Fatal("the item is not in the trash in Drive")
	}
}
