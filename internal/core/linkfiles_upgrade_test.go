package core_test

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestExportsOfAnOlderBuildBecomeLinkFiles(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	doc := env.Emu.WriteGoogleFile(env.Emu.Root(), "Report", mimeDoc)
	env.Emu.WriteGoogleFile(env.Emu.Root(), "Sketch", mimeGoogle+"drawing")
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	makeDatabaseOfAnOlderBuild(t, env)
	cached := contentFile(env, doc)
	if err := os.MkdirAll(filepath.Dir(cached), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("export"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Start()
	names := env.Names("")
	slices.Sort(names)
	if !slices.Equal(names, []string{"Report.gdoc", "Sketch.gdraw"}) {
		t.Fatalf("after the upgrade the root lists %q", names)
	}

	if a := env.MustResolve("Report.gdoc"); a.Size != int64(len(readLinkPage(t, env, a.Ino))) {
		t.Fatalf("the link file shows size %d", a.Size)
	}
	if ps, err := env.FS.Store().Pieces(item(t, env, "Report.gdoc").Ino); err != nil || len(ps) != 0 {
		t.Fatalf("the export is still recorded as cached: %v, %v", ps, err)
	}
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Fatalf("the export is still in the cache: %v", err)
	}
}

func TestHiddenGoogleTypesOfAnOlderBuildBecomeLinkFiles(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteGoogleFile(env.Emu.Root(), "Table", mimeGoogle+"fusiontable")
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil

	for _, q := range []string{
		`UPDATE items SET kind = 2, dname = name, shown_as = '' WHERE id = '` + id + `'`,
		`UPDATE state SET value = '3' WHERE key = 'name_rules'`,
	} {
		if err := execSQL(filepath.Join(env.Dir, "meta.db"), q); err != nil {
			t.Fatal(q, err)
		}
	}
	env.Start()
	if got := env.Names(""); !slices.Equal(got, []string{"Table.glink"}) {
		t.Fatalf("after the upgrade the root lists %q", got)
	}
	if a := env.MustResolve("Table.glink"); a.Mode != syscall.S_IFREG|0o444 {
		t.Fatalf("after the upgrade the mode is %o", a.Mode)
	}
}

func makeDatabaseOfAnOlderBuild(t *testing.T, env *testenv.Env) {
	t.Helper()
	db := env.Dir + "/meta.db"
	for _, q := range []string{
		`UPDATE items SET dname = 'Report.docx' WHERE mime = '` + mimeDoc + `'`,
		`UPDATE items SET kind = 2, dname = name WHERE mime = '` + mimeGoogle + `drawing'`,
		`CREATE TABLE exports (ino INTEGER PRIMARY KEY, size INTEGER NOT NULL, mtime INTEGER NOT NULL)`,
		`INSERT INTO exports SELECT ino, 6, mtime FROM items WHERE mime = '` + mimeDoc + `'`,
		`INSERT INTO pieces(ino, off, len) SELECT ino, 0, 6 FROM items WHERE mime = '` + mimeDoc + `'`,
		`ALTER TABLE items DROP COLUMN rev`,
		`ALTER TABLE items DROP COLUMN lost`,
		`ALTER TABLE items DROP COLUMN service`,
		`PRAGMA user_version = 11`,
	} {
		if err := execSQL(db, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}
