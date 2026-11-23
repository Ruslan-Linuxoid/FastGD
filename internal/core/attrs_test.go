package core_test

import (
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestTimesStayTheSameToTheNanosecond(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Clock.Advance(1234567 * time.Nanosecond)
	env.Start()
	writeFile(t, env, "", "f.txt", []byte("x"))
	created := env.MustResolve("f.txt").Mtime
	if created.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("a new file's time %v is finer than a millisecond", created)
	}
	set := testenv.Epoch().Add(time.Hour + 987654321*time.Nanosecond)
	if err := env.FS.SetMtime(env.MustResolve("f.txt").Ino, set); err != nil {
		t.Fatal(err)
	}
	before := env.MustResolve("f.txt").Mtime
	if !before.Equal(set.Truncate(time.Millisecond)) {
		t.Fatalf("utimes to %v shows %v", set, before)
	}
	uploadAll(t, env)
	if after := env.MustResolve("f.txt").Mtime; !after.Equal(before) {
		t.Fatalf("the upload moved the time from %v to %v", before, after)
	}
	if f := inDrive(t, env, "f.txt"); !f.Modified.Equal(before) {
		t.Fatalf("Drive holds the time %v, want the %v set here", f.Modified, before)
	}
	poll(t, env)
	if back := env.MustResolve("f.txt").Mtime; !back.Equal(before) {
		t.Fatalf("the change from Drive moved the time from %v to %v", before, back)
	}
}

func TestModesOfTheMountComeFromItsOptions(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ file, dir, readOnly uint32 }{
		"not set":         {0o600, 0o700, 0o400},
		"open to a group": {0o640, 0o750, 0o440},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			env.Tune = func(o *core.Options) {
				o.FileMode, o.DirMode = 0, 0
				if name != "not set" {
					o.FileMode, o.DirMode = c.file, c.dir
				}
			}
			dir := env.Emu.Mkdir(env.Emu.Root(), "dir")
			plain := env.Emu.WriteFile(dir, "plain.txt", []byte("x"))
			foreign(env, "theirs.txt", func(f *emulator.File) { f.CannotModify = true })
			env.Emu.WriteGoogleFile(env.Emu.Root(), "Doc", "application/vnd.google-apps.document")
			env.Emu.Put(emulator.File{Name: "link", MimeType: emulator.MimeShortcut, Parents: []string{env.Emu.Root()},
				ShortcutTarget: plain})
			env.Start()
			for path, want := range map[string]uint32{
				"dir":           syscall.S_IFDIR | c.dir,
				"dir/plain.txt": syscall.S_IFREG | c.file,
				"theirs.txt":    syscall.S_IFREG | c.readOnly,
				"Doc.gdoc":      syscall.S_IFREG | c.readOnly,
				"link":          syscall.S_IFLNK | 0o777,
			} {
				if got := env.MustResolve(path).Mode; got != want {
					t.Errorf("%s shows the mode %o, want %o", path, got, want)
				}
			}
		})
	}
}
