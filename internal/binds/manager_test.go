package binds_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/binds"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

type fakeBinder struct {
	bound   [][2]string
	unbound []string

	unbindErr error
}

func (b *fakeBinder) Bind(source, target string) error {
	b.bound = append(b.bound, [2]string{source, target})
	return nil
}

func (b *fakeBinder) Unbind(target string) error {
	b.unbound = append(b.unbound, target)
	return b.unbindErr
}

type stand struct {
	env              *testenv.Env
	work, docs       string
	dir, mnt, places string
}

func newStand(t *testing.T) *stand {
	t.Helper()
	env := testenv.New(t)
	s := &stand{env: env, dir: t.TempDir(), mnt: t.TempDir(), places: t.TempDir()}
	s.work = env.Emu.Mkdir(env.Emu.Root(), "Work")
	s.docs = env.Emu.Mkdir(s.work, "Docs")
	env.Emu.WriteFile(s.docs, "a.txt", []byte("a"))
	env.Start()
	return s
}

func (s *stand) manager(t *testing.T, root bool, helper binds.Binder) *binds.Manager {
	t.Helper()
	m := binds.New(binds.Options{Dir: s.dir, MountPoint: s.mnt, Root: root, Helper: helper,
		Log: slog.New(slog.DiscardHandler)})
	if err := m.Start(s.env.FS); err != nil {
		t.Fatal(err)
	}
	return m
}

func (s *stand) place(name string) string { return filepath.Join(s.places, name) }

func requireRows(t *testing.T, dir string, want ...binds.Row) {
	t.Helper()
	got, err := binds.Load(dir)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("the list holds %+v, %v; want %+v", got, err, want)
	}
}

func TestAnAddedFolderIsBoundAtOnce(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	b := &fakeBinder{}
	m := s.manager(t, true, b)
	if got, err := m.Add("My Drive/Work/Docs", s.place("docs")); err != nil || got != binds.AddedBound {
		t.Fatalf("add: %q, %v", got, err)
	}
	if len(b.bound) != 1 || b.bound[0] != [2]string{filepath.Join(s.mnt, "My Drive/Work/Docs"), s.place("docs")} {
		t.Fatalf("the helper was asked %q", b.bound)
	}
	requireRows(t, s.dir, binds.Row{Folder: s.docs, Target: s.place("docs"), State: binds.Bound})
}

func TestAFolderAddedWithoutTheHelperIsBoundAtTheNextStart(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	m := s.manager(t, true, nil)
	if got, err := m.Add("My Drive/Work", s.place("work")); err != nil || got != binds.AddedPending {
		t.Fatalf("add: %q, %v", got, err)
	}
	requireRows(t, s.dir, binds.Row{Folder: s.work, Target: s.place("work"), State: binds.Pending})
	b := &fakeBinder{}
	s.manager(t, true, b)
	if len(b.bound) != 1 || b.bound[0][1] != s.place("work") {
		t.Fatalf("the next start asked the helper %q", b.bound)
	}
	requireRows(t, s.dir, binds.Row{Folder: s.work, Target: s.place("work"), State: binds.Bound})
}

func TestMountAddRefuses(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	b := &fakeBinder{}
	m := s.manager(t, true, b)
	if _, err := m.Add("My Drive/Work", s.place("work")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.place("full/x"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, target string }{
		{"My Drive/Work/Docs/a.txt", s.place("a")},
		{"My Drive/Work/None", s.place("none")},
		{"My Drive/Work/Docs", s.place("full")},
		{"My Drive/Work/Docs", s.place("no/such")},
		{"My Drive/Work/Docs", filepath.Join(s.mnt, "in")},
		{"My Drive/Work/Docs", s.place("work")},
		{"My Drive/Work/Docs", s.place("work/in")},
	} {
		if _, err := m.Add(c.path, c.target); err == nil {
			t.Fatalf("%s was added at %s", c.path, c.target)
		}
	}
	if _, err := s.manager(t, false, nil).Add("My Drive/Work/Docs", s.place("docs")); !errors.Is(err, binds.ErrNoRoot) {
		t.Fatalf("a daemon without root answered %v", err)
	}
	if len(b.bound) != 1 {
		t.Fatalf("the helper was asked %q", b.bound)
	}
}

func TestABindingGoneIsBoundAgain(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	b := &fakeBinder{}
	m := s.manager(t, true, b)
	if _, err := m.Add("My Drive/Work", s.place("work")); err != nil {
		t.Fatal(err)
	}

	m.Tick()
	if len(b.bound) != 2 || b.bound[1][1] != s.place("work") {
		t.Fatalf("the helper was asked %q", b.bound)
	}
}

func TestAMovedFolderStaysInTheList(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	m := s.manager(t, true, &fakeBinder{})
	if _, err := m.Add("My Drive/Work/Docs", s.place("docs")); err != nil {
		t.Fatal(err)
	}
	s.env.Emu.Update(s.docs, func(f *emulator.File) { f.Name, f.Parents = "Papers", []string{s.env.Emu.Root()} })
	poll(t, s.env)
	m.Tick()
	if got, err := m.List(); err != nil || len(got) != 1 || got[0].Path != "My Drive/Papers" ||
		got[0].State != binds.Bound {
		t.Fatalf("the list is %+v, %v", got, err)
	}
}

func TestTheBindingOfAFolderGoneIsIgnored(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		gone func(e *emulator.Emulator, id string)
		why  string
	}{
		"trashed": {func(e *emulator.Emulator, id string) { e.Update(id, func(f *emulator.File) { f.Trashed = true }) },
			core.GoneTrash},
		"deleted": {func(e *emulator.Emulator, id string) { e.Delete(id) }, core.GoneDeleted},
		"left My Drive": {func(e *emulator.Emulator, id string) {
			e.TellParentless(emulator.ParentlessRemoved)
			e.Update(id, func(f *emulator.File) { f.Parents = nil })
		}, core.GoneDeleted},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newStand(t)
			m := s.manager(t, true, &fakeBinder{})
			if _, err := m.Add("My Drive/Work/Docs", s.place("docs")); err != nil {
				t.Fatal(err)
			}
			c.gone(s.env.Emu, s.docs)
			poll(t, s.env)
			m.Tick()
			want := binds.Row{Folder: s.docs, Target: s.place("docs"), State: binds.Ignored, Reason: c.why}
			requireRows(t, s.dir, want)
			b := &fakeBinder{}
			s.manager(t, true, b).Tick()
			if len(b.bound) != 0 {
				t.Fatalf("the next start bound %q", b.bound)
			}
			requireRows(t, s.dir, want)
		})
	}
}

func TestMountRemoveTakesTheRowOff(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	m := s.manager(t, true, &fakeBinder{})
	if _, err := m.Add("My Drive/Work", s.place("work")); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(s.place("work") + "/"); err != nil {
		t.Fatal(err)
	}
	requireRows(t, s.dir)
	if err := m.Remove(s.place("work")); err == nil {
		t.Fatal("a place not in the list was removed")
	}
}

func poll(t *testing.T, env *testenv.Env) {
	t.Helper()
	if err := env.FS.PollChanges(t.Context()); err != nil {
		t.Fatal(err)
	}
}
