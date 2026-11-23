package core_test

import (
	"context"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const (
	mimeGoogle = "application/vnd.google-apps."
	mimeDoc    = mimeGoogle + "document"
	mimeSheet  = mimeGoogle + "spreadsheet"
	mimeSlides = mimeGoogle + "presentation"
)

func contentRequests(env *testenv.Env) int {
	n := 0
	for _, r := range env.Emu.Requests() {
		if r.Query.Get("alt") == "media" {
			n++
		}
	}
	return n
}

func TestGoogleTypesShowAsLinkFiles(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	want := make([]string, 0, 10)
	for kind, ext := range map[string]string{
		"document": ".gdoc", "spreadsheet": ".gsheet", "presentation": ".gslides", "drawing": ".gdraw", "form": ".gform",
		"site": ".gsite", "map": ".gmap", "script": ".gscript", "jam": ".gjam",
	} {
		env.Emu.WriteGoogleFile(root, "My "+kind, mimeGoogle+kind)
		want = append(want, "My "+kind+ext)
	}

	env.Emu.WriteGoogleFile(root, "Table", mimeGoogle+"fusiontable")
	want = append(want, "Table.glink")
	env.Start()
	names := env.Names("")
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("the root lists %q, want %q", names, want)
	}
	for _, name := range names {
		a := env.MustResolve(name)
		if page := readLinkPage(t, env, a.Ino); a.Mode != syscall.S_IFREG|0o444 || a.Size != int64(len(page)) {
			t.Fatalf("%s: mode %o, size %d, want a read-only file of the size of its page, %d", name, a.Mode, a.Size,
				len(page))
		}
	}
}

func TestLinkFilesReadAsAPageAndTakeNoWrites(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteGoogleFile(env.Emu.Root(), "Report", mimeDoc)
	env.Start()
	env.Emu.ResetRequests()
	ino := env.MustResolve("Report.gdoc").Ino
	page := string(readLinkPage(t, env, ino))
	url := "https://docs.google.com/document/d/" + id
	if !strings.HasPrefix(page, "<!DOCTYPE html>") || !strings.Contains(page, `content="0; url=`+url+`"`) ||
		!strings.Contains(page, `<a href="`+url+`">`) {
		t.Fatalf("the link file reads %q", page)
	}
	if n := len(env.Emu.Requests()); n != 0 {
		t.Fatalf("the page of a link file took %d requests to Drive", n)
	}
	for _, flags := range []uint32{syscall.O_WRONLY, syscall.O_RDWR, syscall.O_RDONLY | syscall.O_TRUNC} {
		if _, err := env.FS.Open(context.Background(), ino, flags); err != syscall.EACCES {
			t.Fatalf("open with %#x got %v, want EACCES", flags, err)
		}
	}
	if err := env.FS.Truncate(context.Background(), ino, 0); err != syscall.EACCES {
		t.Fatalf("truncate got %v, want EACCES", err)
	}
	if err := env.FS.SetMtime(ino, time.Now()); err != syscall.EACCES {
		t.Fatalf("setting the time got %v, want EACCES", err)
	}
	if n := contentRequests(env); n != 0 {
		t.Fatalf("%d requests for the content of a link file", n)
	}
}

func readLinkPage(t *testing.T, env *testenv.Env, ino uint64) []byte {
	t.Helper()
	fh, err := env.FS.Open(t.Context(), ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatalf("open the link file: %v", err)
	}
	defer env.FS.Release(fh)
	var page []byte
	buf := make([]byte, 100)
	for {
		n, err := env.FS.Read(t.Context(), fh, int64(len(page)), buf)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return page
		}
		page = append(page, buf[:n]...)
	}
}

func requireLinkFileAttributes(t *testing.T, env *testenv.Env, name, want string) {
	t.Helper()
	ino := env.MustResolve(name).Ino
	list, err := env.FS.ListXattr(ino)
	if err != nil || !slices.Equal(list, []string{"user.drive.gdoc", "user.drive.item_open_url", "user.drive.email"}) {
		t.Fatalf("%s lists the attributes %q, %v", name, list, err)
	}
	for attr, want := range map[string]string{
		"user.drive.item_open_url": want, "user.drive.gdoc": "1", "user.drive.email": emulator.Email,
	} {
		if got, err := env.FS.GetXattr(t.Context(), ino, attr); err != nil || got != want {
			t.Fatalf("%s of %s = %q, %v, want %q", attr, name, got, err, want)
		}
	}
	_, err = env.FS.GetXattr(t.Context(), ino, "user.drive.other")
	testenv.RequireErrno(t, err, syscall.ENODATA)
	_, err = env.FS.GetXattr(t.Context(), ino, "user.x")
	testenv.RequireErrno(t, err, syscall.ENOTSUP)
}

func requireNoAttributes(t *testing.T, env *testenv.Env, name string) {
	t.Helper()
	ino := env.MustResolve(name).Ino
	if list, err := env.FS.ListXattr(ino); err != nil || len(list) != 0 {
		t.Fatalf("%q lists the attributes %q, %v", name, list, err)
	}
	_, err := env.FS.GetXattr(t.Context(), ino, "user.drive.gdoc")
	testenv.RequireErrno(t, err, syscall.ENOTSUP)
}

func TestLinkFilesCarryTheirAddressInAttributes(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	ids := map[string]string{}
	for _, kind := range []string{"document", "spreadsheet", "presentation", "drawing", "form"} {
		ids[kind] = env.Emu.WriteGoogleFile(root, kind, mimeGoogle+kind)
	}
	env.Emu.WriteFile(root, "plain.txt", []byte("x"))
	env.Start()
	if err := env.FS.RefreshQuota(t.Context()); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"document.gdoc":        "https://docs.google.com/document/d/" + ids["document"],
		"spreadsheet.gsheet":   "https://docs.google.com/spreadsheets/d/" + ids["spreadsheet"],
		"presentation.gslides": "https://docs.google.com/presentation/d/" + ids["presentation"],
		"drawing.gdraw":        "https://docs.google.com/drawings/d/" + ids["drawing"],

		"form.gform": emulator.ViewLink(mimeGoogle+"form", ids["form"]),
	} {
		requireLinkFileAttributes(t, env, name, want)
	}

	env.Emu.SetOffline(true)
	_, err := env.FS.GetXattr(t.Context(), env.MustResolve("form.gform").Ino, "user.drive.item_open_url")
	testenv.RequireErrno(t, err, syscall.EIO)
	if got, err := env.FS.GetXattr(t.Context(), env.MustResolve("document.gdoc").Ino,
		"user.drive.item_open_url"); err != nil || got == "" {
		t.Fatalf("the address of a Doc without the network: %q, %v", got, err)
	}
	env.Emu.SetOffline(false)

	for _, name := range []string{"plain.txt", ""} {
		requireNoAttributes(t, env, name)
	}
}

func TestLinkFilesTellTheAccountOnceKnown(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteGoogleFile(env.Emu.Root(), "Report", mimeDoc)
	env.Start()
	ino := env.MustResolve("Report.gdoc").Ino
	if list, err := env.FS.ListXattr(ino); err != nil || !slices.Equal(list, []string{"user.drive.gdoc",
		"user.drive.item_open_url"}) {
		t.Fatalf("before the account is known the attributes are %q, %v", list, err)
	}
	_, err := env.FS.GetXattr(t.Context(), ino, "user.drive.email")
	testenv.RequireErrno(t, err, syscall.ENODATA)
	if err := env.FS.RefreshQuota(t.Context()); err != nil {
		t.Fatal(err)
	}
	env.Restart()
	env.Emu.SetOffline(true)
	if got, err := env.FS.GetXattr(t.Context(), env.MustResolve("Report.gdoc").Ino, "user.drive.email"); err != nil ||
		got != emulator.Email {
		t.Fatalf("after a restart without the network the account is %q, %v", got, err)
	}
}

func TestLinkFilesRenameMoveAndTrashAsFiles(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteGoogleFile(env.Emu.Root(), "Report", mimeDoc)
	env.Start()
	mkdir(t, env, "", "dir")
	rename(t, env, "", "Report.gdoc", "dir", "Notes.GDoc")
	if got := env.Names("dir"); !slices.Equal(got, []string{"Notes.gdoc"}) {
		t.Fatalf("dir lists %q", got)
	}
	uploadAll(t, env)
	f, ok := env.Emu.Get(id)
	if !ok || f.Name != "Notes" || f.Parents[0] != item(t, env, "dir").ID {
		t.Fatalf("Drive has the document as %q under %v", f.Name, f.Parents)
	}
	if err := env.FS.Unlink(env.MustResolve("dir").Ino, "Notes.gdoc"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed {
		t.Fatal("the document is not in the trash in Drive")
	}
}

func TestCreatingALinkFileIsRefused(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	root := env.MustResolve("").Ino
	for _, name := range []string{"New.gdoc", "New.GSheet", "New.gslides", "New.gform", "New.glink"} {
		_, _, err := env.FS.Create(root, name, syscall.O_WRONLY)
		testenv.RequireErrno(t, err, syscall.EPERM)
	}
	if got := env.Names(""); len(got) != 0 {
		t.Fatalf("the root lists %q", got)
	}
	writeFile(t, env, "", "gdoc", []byte("a file whose name only ends like one"))
}

func TestRenameToALinkNameIsRefused(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "plain.txt", []byte("x"))
	env.Emu.WriteGoogleFile(env.Emu.Root(), "Report", mimeDoc)
	env.Start()
	root := env.MustResolve("").Ino
	for _, name := range []string{"plain.gdoc", "plain.GSheet", "plain.glink"} {
		testenv.RequireErrno(t, env.FS.Rename(root, "plain.txt", root, name, 0), syscall.EPERM)
	}
	rename(t, env, "", "Report.gdoc", "", "Notes.gdoc")
	if got := env.Names(""); !slices.Equal(got, []string{"Notes.gdoc", "plain.txt"}) {
		t.Fatalf("the root lists %q", got)
	}
}
