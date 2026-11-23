package core_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestUpgradeFromTheBlockCache(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	clean := testenv.RandomBytes(mib, 13)
	env.Emu.WriteFile(env.Emu.Root(), "clean.bin", clean)
	env.Start()
	readAll(t, env, "clean.bin")
	dirty := testenv.RandomBytes(3*mib+7, 14)
	writeFile(t, env, "", "dirty.bin", dirty)
	cleanItem := item(t, env, "clean.bin")
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	db := filepath.Join(env.Dir, "meta.db")
	if err := asBlockSchema(db); err != nil {
		t.Fatal(err)
	}

	env.Start()
	if _, err := os.Stat(contentPath(env, cleanItem.Item)); !os.IsNotExist(err) {
		t.Fatalf("the clean file's old content is still there: %v", err)
	}
	if got := readAll(t, env, "dirty.bin"); !bytes.Equal(got, dirty) {
		t.Fatal("the dirty file lost bytes in the upgrade")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "dirty.bin"); md5Of(f.Content) != md5Of(dirty) {
		t.Fatal("the dirty file went up different")
	}
	if got := readAll(t, env, "clean.bin"); !bytes.Equal(got, clean) {
		t.Fatal("the clean file does not read back from Drive")
	}
}

func TestUpgradeCutOffHalfWay(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", []byte("content"))
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	db := filepath.Join(env.Dir, "meta.db")
	if err := asBlockSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := execSQL(db, `DROP TABLE blocks`); err != nil {
		t.Fatal(err)
	}
	env.Start()
	if got := readAll(t, env, "f.bin"); string(got) != "content" {
		t.Fatalf("after the upgrade the file reads %q", got)
	}
}

func TestUpgradeKilledBeforeTheOldContentWasDropped(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(64*kib, 21)
	env.Emu.WriteFile(env.Emu.Root(), "clean.bin", content)
	env.Start()
	readAll(t, env, "clean.bin")
	atRest(t, env)
	mine := testenv.RandomBytes(5000, 22)
	writeFile(t, env, "", "mine.bin", mine)
	clean, dirty := contentPath(env, item(t, env, "clean.bin").Item), contentPath(env, item(t, env, "mine.bin").Item)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	db := filepath.Join(env.Dir, "meta.db")
	if err := execSQL(db, `INSERT INTO state(key, value) VALUES ('drop_clean_content', '1')`); err != nil {
		t.Fatal(err)
	}
	env.Start()
	if _, err := os.Lstat(clean); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the content of the clean file is not dropped: %v", err)
	}
	if got, err := os.ReadFile(dirty); err != nil || !bytes.Equal(got, mine) {
		t.Fatalf("the content of the file changed here is not kept: %v", err)
	}
	if _, pending, err := env.FS.Store().GetState("drop_clean_content"); err != nil || pending {
		t.Fatalf("the drop is still to do after the start that did it: %v", err)
	}
	if got := readAll(t, env, "clean.bin"); !bytes.Equal(got, content) {
		t.Fatal("the clean file does not read as Drive has it")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "mine.bin"); !bytes.Equal(f.Content, mine) {
		t.Fatal("the file changed here went to Drive different")
	}
}
