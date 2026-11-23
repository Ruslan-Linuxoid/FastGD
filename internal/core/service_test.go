package core_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func driveNames(env *testenv.Env) []string {
	all := env.Emu.All()
	out := make([]string, 0, len(all))
	for _, f := range all {
		out = append(out, f.Name)
	}
	slices.Sort(out)
	return out
}

func unlink(t *testing.T, env *testenv.Env, dir, name string) {
	t.Helper()
	if err := env.FS.Unlink(env.MustResolve(dir).Ino, name); err != nil {
		t.Fatalf("unlink %s/%s: %v", dir, name, err)
	}
}

func TestLockFileOfLibreOfficeStaysHere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.ResetRequests()
	writeFile(t, env, "", ".~lock.a.odt#", []byte(",user,host,20.11.2026 10:00,file:///home/user/.config/libreoffice/4;"))
	writeFile(t, env, "", "a.odt", []byte("the document"))
	uploadAll(t, env)
	if got := readAll(t, env, ".~lock.a.odt#"); !strings.HasPrefix(string(got), ",user,host") {
		t.Fatal("the lock file does not read back")
	}
	unlink(t, env, "", ".~lock.a.odt#")
	uploadAll(t, env)
	if names := driveNames(env); !slices.Equal(names, []string{"a.odt"}) {
		t.Fatalf("Drive holds %q, want the document alone", names)
	}
	if n := writesToDrive(env); n != 1 {
		t.Fatalf("%d requests changed Drive, want the one that made the document", n)
	}
	if names := env.Names(""); !slices.Equal(names, []string{"a.odt"}) {
		t.Fatalf("the mount shows %q after the lock was removed", names)
	}
}

func TestSwapFileOfVimStaysHereAndOutlivesARestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.ResetRequests()
	_, fh := createFile(t, env, "", ".a.txt.swp")
	write(t, env, fh, 0, []byte("b0VIM 9.1"))
	write(t, env, fh, 4096, []byte("recovery data"))
	env.FS.Release(fh)
	uploadAll(t, env)
	env.Restart()
	uploadAll(t, env)
	got := readAll(t, env, ".a.txt.swp")
	if len(got) != 4096+13 || !bytes.HasPrefix(got, []byte("b0VIM 9.1")) || !bytes.HasSuffix(got,
		[]byte("recovery data")) {
		t.Fatalf("after a restart the swap file reads %d bytes", len(got))
	}
	if it := item(t, env, ".a.txt.swp"); !it.Service {
		t.Fatal("the swap file is not marked as a service file")
	}
	writeFile(t, env, "", "a.txt", []byte("saved"))
	unlink(t, env, "", ".a.txt.swp")
	uploadAll(t, env)
	if names := driveNames(env); !slices.Equal(names, []string{"a.txt"}) {
		t.Fatalf("Drive holds %q, want the saved file alone", names)
	}
	if n := writesToDrive(env); n != 1 {
		t.Fatalf("%d requests changed Drive, want the one that made the file", n)
	}
	if n := lostFiles(t, env); n != 0 {
		t.Fatalf("status counts %d files in Lost and Found", n)
	}
}

func TestDownloadOfChromeGoesUpOnceItIsRenamed(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	env.Emu.ResetRequests()
	content := testenv.RandomBytes(sessionFrom, 950)
	writeFile(t, env, "", "b.iso.crdownload", content)
	uploadAll(t, env)
	if reqs := env.Emu.Requests(); len(reqs) != 0 {
		t.Fatalf("before the rename Drive got %d requests, the first %+v", len(reqs), reqs[0])
	}
	rename(t, env, "", "b.iso.crdownload", "", "b.iso")
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.sessions != 1 || u.simple != 0 {
		t.Fatalf("after the rename the content went up as %+v, want one session", u)
	}
	if names := driveNames(env); !slices.Equal(names, []string{"b.iso"}) {
		t.Fatalf("Drive holds %q", names)
	}
	if f := inDrive(t, env, "b.iso"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("Drive holds other bytes than the download")
	}
}

func TestDownloadRenamedOverAFileOfDriveBecomesItsContent(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	old := env.Emu.WriteFile(env.Emu.Root(), "b.iso", []byte("old"))
	env.Start()
	writeFile(t, env, "", "b.iso.crdownload", []byte("new"))
	rename(t, env, "", "b.iso.crdownload", "", "b.iso")
	uploadAll(t, env)
	requireSaved(t, env, old, "b.iso", "new", 2)
	if it := item(t, env, "b.iso"); it.Service {
		t.Fatal("the file is still a service item")
	}
}

func TestWhatLiesInAServiceFolderStaysHere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.ResetRequests()
	mkdir(t, env, "", ".DS_Store")
	writeFile(t, env, ".DS_Store", "inside.txt", []byte("inside"))
	mkdir(t, env, ".DS_Store", "deeper")
	writeFile(t, env, ".DS_Store/deeper", "more.txt", []byte("more"))
	writeFile(t, env, "", "one.swp", []byte("swap"))
	rename(t, env, "", "one.swp", "", "two.swp")
	uploadAll(t, env)
	if reqs := env.Emu.Requests(); len(reqs) != 0 {
		t.Fatalf("Drive got %d requests for service items, the first %+v", len(reqs), reqs[0])
	}
	for _, p := range []string{".DS_Store/inside.txt", ".DS_Store/deeper/more.txt", "two.swp"} {
		if it := item(t, env, p); !it.Service {
			t.Fatalf("%s is not a service item", p)
		}
	}

	env.Emu.WriteFile(env.Emu.Root(), "real.txt", []byte("real"))
	poll(t, env)
	err := env.FS.Rename(env.MustResolve("").Ino, "real.txt", env.MustResolve(".DS_Store").Ino, "real.txt", 0)
	if err != syscall.EPERM {
		t.Fatalf("a move of a file of Drive into a service folder got %v, want EPERM", err)
	}
}

func TestFileOfDriveUnderAServiceNameIsOrdinary(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	swp := env.Emu.WriteFile(env.Emu.Root(), "notes.swp", []byte("from Drive"))
	doc := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("doc"))
	env.Start()
	if names := env.Names(""); !slices.Equal(names, []string{"doc.txt", "notes.swp"}) {
		t.Fatalf("the mount shows %q", names)
	}
	fh := openWriter(t, env, "notes.swp", 0)
	write(t, env, fh, 0, []byte("edited"))
	env.FS.Release(fh)
	rename(t, env, "", "doc.txt", "", "desktop.ini")
	uploadAll(t, env)
	if f, _ := env.Emu.Get(swp); !strings.HasPrefix(string(f.Content), "edited") {
		t.Fatalf("the edit of the file of Drive did not go up: %q", f.Content)
	}
	if f, _ := env.Emu.Get(doc); f.Name != "desktop.ini" || f.Trashed {
		t.Fatalf("the rename to a service name left the file as %q in Drive, trashed %v", f.Name, f.Trashed)
	}
}

func TestServiceFilesOfAFolderDeletedInDriveAreThrownAway(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	dir := env.Emu.Mkdir(env.Emu.Root(), "work")
	env.Start()
	writeFile(t, env, "work", ".~lock.a.odt#", []byte("lock"))
	writeFile(t, env, "work", "draft.txt", []byte("draft"))
	env.Emu.Update(dir, func(f *emulator.File) { f.Trashed = true })
	poll(t, env)
	if names := lostNames(env, lostDir+"/work"); len(names) != 1 || !conflictName("draft", ".txt").MatchString(names[0]) {
		t.Fatalf("Lost and Found holds %q of the deleted folder, want the draft alone", names)
	}
	var left int
	if err := execSQLScan(env.Dir+"/meta.db", `SELECT count(*) FROM items WHERE service = 1`, &left); err != nil ||
		left != 0 {
		t.Fatalf("%d service items are left of the deleted folder, %v", left, err)
	}
}

func TestServiceFilesAreNotCountedAsWaitingForDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "one.swp", []byte("swap"))
	writeFile(t, env, "", "real.txt", []byte("real"))
	db, err := meta.OpenReadOnly(env.Dir + "/meta.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := db.ReadStatus(env.Clock.Now())
	if err != nil || st.DirtyFiles != 1 {
		t.Fatalf("status counts %d dirty files, %v, want the one that goes to Drive", st.DirtyFiles, err)
	}
	env.FS.SyncOnce(context.Background())
}

func TestRenamesThatDoNotCrossLeaveItemsWhatTheyWere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", ".a.txt.swp", []byte("swap"))
	writeFile(t, env, "", "a.txt", []byte("text"))
	root := env.MustResolve("").Ino
	if err := env.FS.Rename(root, ".a.txt.swp", root, "a.txt", unix.RENAME_NOREPLACE); err != syscall.EEXIST {
		t.Fatalf("a rename over a file with RENAME_NOREPLACE got %v, want EEXIST", err)
	}
	if it := item(t, env, ".a.txt.swp"); !it.Service {
		t.Fatal("the refused rename took the swap file out of the service files")
	}

	writeFile(t, env, "", "notes.txt", []byte("notes"))
	rename(t, env, "", "notes.txt", "", "notes.swp")
	uploadAll(t, env)
	if names := driveNames(env); !slices.Equal(names, []string{"a.txt", "notes.swp"}) {
		t.Fatalf("Drive holds %q, want the text and the notes under their new name", names)
	}

	mkdir(t, env, "", ".DS_Store")
	if err := env.FS.Rename(root, "notes.swp", env.MustResolve(".DS_Store").Ino, "notes.swp", 0); err != syscall.EPERM {
		t.Fatalf("a move of a file Drive holds into a service folder got %v, want EPERM", err)
	}
	writeFile(t, env, "", "fresh.txt", []byte("fresh"))
	rename(t, env, "", "fresh.txt", ".DS_Store", "fresh.txt")
	uploadAll(t, env)
	if it := item(t, env, ".DS_Store/fresh.txt"); !it.Service {
		t.Fatal("a new file moved into a service folder is not a service file")
	}
	if names := driveNames(env); !slices.Equal(names, []string{"a.txt", "notes.swp"}) {
		t.Fatalf("Drive holds %q after the move into the service folder", names)
	}
}
