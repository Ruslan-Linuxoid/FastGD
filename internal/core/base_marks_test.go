package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func olderMark(t *testing.T, env *testenv.Env, id, rev string) {
	t.Helper()
	env.Emu.KeepRevision(id, rev)
	if err := env.FS.Store().KeepRevision(id, rev); err != nil {
		t.Fatal(err)
	}
}

func TestMarkOfAnOlderRunIsTriedAndLogged(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	want := editedFromDrive(t, env, 256<<10, 8192, 350)
	id := item(t, env, "f.bin").ID
	base := inDrive(t, env, "f.bin").HeadRevision
	olderMark(t, env, id, base)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(1000, 352) })
	env.FS.FillBases(context.Background())
	requireWholeOffline(t, env, want)
	if len(env.Logs("the revision stays marked Keep Forever")) == 0 {
		t.Fatal("the log does not tell that the revision stays marked")
	}
	for _, r := range env.Emu.Revisions(id) {
		if r.Keep != (r.ID == base) {
			t.Fatalf("revision %s has the mark %v", r.ID, r.Keep)
		}
	}
	if kept, err := env.FS.Store().KeptRevisions(); err != nil || len(kept) != 0 {
		t.Fatalf("the database still tells of marked revisions %v: %v", kept, err)
	}
}

func TestMarkTheUserSetStays(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	want := editedFromDrive(t, env, 256<<10, 8192, 360)
	id := item(t, env, "f.bin").ID
	base := inDrive(t, env, "f.bin").HeadRevision
	env.Emu.KeepRevision(id, base)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(1000, 362) })
	env.FS.FillBases(context.Background())
	requireWholeOffline(t, env, want)
	for _, c := range revisionCalls(env) {
		if c == "PATCH" {
			t.Fatal("the daemon changed the mark of a revision the user marked")
		}
	}
}

func TestFetchOfTheBaseGoesOnAfterARestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	want := editedFromDrive(t, env, 256<<10, 8192, 370)
	env.Restart()
	env.FS.FillBases(context.Background())
	requireWholeOffline(t, env, want)
}

func TestDriveGivesARevisionWithoutItsMark(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(10000, 380)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	rev := env.Emu.Revisions(id)[0].ID
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("new") })
	ctx := context.Background()
	var got []byte
	sink := func(b []byte) error { got = append(got, b...); return nil }
	if err := env.Drive.StreamRevision(ctx, id, rev, 100, 5000, sink); err != nil || !bytes.Equal(got, content[100:5100]) {
		t.Fatalf("the older revision without the mark: %v, %d bytes", err, len(got))
	}
	if err := env.Drive.KeepRevision(ctx, id, rev, true); err != nil {
		t.Fatal(err)
	}
	if kept, err := env.Drive.RevisionKept(ctx, id, rev); err != nil || !kept {
		t.Fatalf("the mark reads %v, %v", kept, err)
	}
	if err := env.Drive.KeepRevision(ctx, id, rev, false); err == nil {
		t.Fatal("Drive took the mark off an older revision")
	}
	head := inDrive(t, env, "f.bin").HeadRevision
	if err := env.Drive.KeepRevision(ctx, id, head, true); err != nil {
		t.Fatal(err)
	}
	if err := env.Drive.KeepRevision(ctx, id, head, false); err != nil {
		t.Fatalf("the mark off the head revision: %v", err)
	}
	if _, err := env.Drive.RevisionKept(ctx, id, "none"); !errors.Is(err, drive.ErrNoRevision) {
		t.Fatalf("a revision Drive does not hold: %v", err)
	}
}

func TestBaseFetchGoesAgainAfterTwoSeconds(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	want := editedFromDrive(t, env, 256<<10, 8192, 360)
	env.Emu.SetOffline(true)
	env.FS.FillBases(context.Background())
	env.Emu.SetOffline(false)
	env.Emu.ResetRequests()
	env.Clock.Advance(1900 * time.Millisecond)
	env.FS.FillBases(context.Background())
	if n := len(env.Emu.Requests()); n != 0 {
		t.Fatalf("%d requests for the base 1.9 s after the fetch failed", n)
	}
	env.Clock.Advance(600 * time.Millisecond)
	env.FS.FillBases(context.Background())
	requireWholeOffline(t, env, want)
}
