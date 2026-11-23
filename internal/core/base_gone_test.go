package core_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestEditWithoutItsBaseOfAFileDeletedForGoodIsDropped(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	editedFromDrive(t, env, 256<<10, 8192, 336)
	id := item(t, env, "f.bin").ID
	ino := env.MustResolve("f.bin").Ino
	env.Emu.Delete(id)
	poll(t, env)
	uploadAll(t, env)
	if names := env.Names(""); len(names) != 0 {
		t.Fatalf("the root holds %q, want nothing: neither the file nor Lost and Found", names)
	}
	if _, err := env.FS.Store().ByIno(meta.Ino(ino)); err == nil {
		t.Fatal("the row of the file is still there")
	}
	if len(env.Logs("f.bin")) == 0 || len(env.Logs("deleted in Drive for good")) == 0 {
		t.Fatal("the log does not tell of the edit that was dropped, with its path and why")
	}
	if n := writesToDrive(env); n != 0 {
		t.Fatalf("%d requests changed Drive", n)
	}
}

func TestRevisionDriveDeletedLeavesTheFileDirtyWithTheReason(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	editedFromDrive(t, env, 256<<10, 8192, 340)
	id := item(t, env, "f.bin").ID
	base := inDrive(t, env, "f.bin").HeadRevision
	changed := testenv.RandomBytes(256<<10, 342)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = changed })
	env.Emu.DeleteRevision(id, base)
	env.FS.FillBases(context.Background())
	if len(env.Logs("Drive deleted revision "+base)) == 0 {
		t.Fatal("the log does not tell that Drive deleted the revision")
	}
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if it := item(t, env, "f.bin"); !it.Dirty || !strings.Contains(it.LastError, "Drive deleted revision "+base) {
		t.Fatalf("the file is dirty=%v with error %q", it.Dirty, it.LastError)
	}
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, changed) {
		t.Fatal("a file with a part of its base lost went to Drive")
	}

	env.Emu.ResetRequests()
	env.Clock.Advance(time.Hour)
	env.FS.FillBases(context.Background())
	if n := len(env.Emu.Requests()); n != 0 {
		t.Fatalf("%d more requests for a base Drive gives no more", n)
	}
}
