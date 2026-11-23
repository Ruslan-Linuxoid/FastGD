package core_test

import (
	"context"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestUpgradeAddsRights(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f.txt", []byte("x"))
	foreign(env, "kept.txt", func(f *emulator.File) { f.CannotTrash = true })
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	db := env.Dir + "/meta.db"
	for _, q := range []string{
		`ALTER TABLE items DROP COLUMN denied`, `ALTER TABLE items DROP COLUMN base`,
		`ALTER TABLE items DROP COLUMN base_tail`, `ALTER TABLE items DROP COLUMN create_tried`,
		`ALTER TABLE items DROP COLUMN rev`, `ALTER TABLE items DROP COLUMN lost`, `ALTER TABLE items DROP COLUMN service`,
		`PRAGMA user_version = 7`,
	} {
		if err := execSQL(db, q); err != nil {
			t.Fatal(err)
		}
	}
	env.Start()
	if got := readAll(t, env, "f.txt"); string(got) != "x" {
		t.Fatalf("after the upgrade the file reads %q", got)
	}
	writeFile(t, env, "", "g.txt", []byte("y"))
	if err := env.FS.Unlink(env.MustResolve("").Ino, "kept.txt"); err != syscall.EACCES {
		t.Fatalf("after the upgrade unlink of a file the user may not trash got %v, want EACCES", err)
	}
}

func TestUpgradeLearnsWhatMayNotBeDownloaded(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	foreign(env, "closed.txt", func(f *emulator.File) { f.CannotDownload = true })
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	db := env.Dir + "/meta.db"
	for _, q := range []string{`UPDATE items SET denied = 0`, `PRAGMA user_version = 17`} {
		if err := execSQL(db, q); err != nil {
			t.Fatal(err)
		}
	}
	env.Start()
	poll(t, env)
	_, err := env.FS.Open(context.Background(), env.MustResolve("closed.txt").Ino, syscall.O_RDONLY)
	testenv.RequireErrno(t, err, syscall.EACCES)

	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	for _, q := range []string{`UPDATE items SET denied = 0`, `ALTER TABLE items DROP COLUMN service`,
		`PRAGMA user_version = 16`} {
		if err := execSQL(db, q); err != nil {
			t.Fatal(err)
		}
	}
	env.Start()
	poll(t, env)
	_, err = env.FS.Open(context.Background(), env.MustResolve("closed.txt").Ino, syscall.O_RDONLY)
	testenv.RequireErrno(t, err, syscall.EACCES)
}
