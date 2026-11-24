package binds_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/binds"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func everywhere(string) bool { return true }

func TestMountRemoveUnbinds(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	b := &fakeBinder{}
	m := s.manager(t, true, b)
	if _, err := m.Add("My Drive/Work", s.place("work")); err != nil {
		t.Fatal(err)
	}
	binds.SetOurs(m, everywhere)
	if err := m.Remove(s.place("work")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.unbound, []string{s.place("work")}) {
		t.Fatalf("the helper unbound %q", b.unbound)
	}
	requireRows(t, s.dir)
}

func TestABindingThatDoesNotComeUndoneIsNotIgnoredYet(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	b := &fakeBinder{}
	m := s.manager(t, true, b)
	if _, err := m.Add("My Drive/Work/Docs", s.place("docs")); err != nil {
		t.Fatal(err)
	}
	binds.SetOurs(m, everywhere)
	b.unbindErr = errors.New("busy")
	s.env.Emu.Update(s.docs, func(f *emulator.File) { f.Trashed = true })
	poll(t, s.env)
	m.Tick()
	if len(b.unbound) != 1 {
		t.Fatalf("the helper was asked to unbind %q", b.unbound)
	}
	requireRows(t, s.dir, binds.Row{Folder: s.docs, Target: s.place("docs"), State: binds.Bound})
	b.unbindErr = nil
	m.Tick()
	requireRows(t, s.dir, binds.Row{Folder: s.docs, Target: s.place("docs"), State: binds.Ignored,
		Reason: core.GoneTrash})
}

func TestMountRemoveTakesOffPendingAndIgnoredRows(t *testing.T) {
	t.Parallel()
	s := newStand(t)
	waiting := s.manager(t, true, nil)
	if _, err := waiting.Add("My Drive/Work", s.place("work")); err != nil {
		t.Fatal(err)
	}
	if err := waiting.Remove(s.place("work")); err != nil {
		t.Fatal(err)
	}
	requireRows(t, s.dir)

	b := &fakeBinder{}
	m := s.manager(t, true, b)
	if _, err := m.Add("My Drive/Work/Docs", s.place("docs")); err != nil {
		t.Fatal(err)
	}
	s.env.Emu.Delete(s.docs)
	poll(t, s.env)
	m.Tick()
	requireRows(t, s.dir, binds.Row{Folder: s.docs, Target: s.place("docs"), State: binds.Ignored,
		Reason: core.GoneDeleted})
	binds.SetOurs(m, everywhere)
	if err := m.Remove(s.place("docs")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.unbound, []string{s.place("docs")}) {
		t.Fatalf("the helper unbound %q", b.unbound)
	}
	requireRows(t, s.dir)
}
