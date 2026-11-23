package core_test

import (
	"context"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestNamesUpTo1024BytesShowAsTheyAre(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	long := strings.Repeat("я", 300) + ".txt"
	env.Emu.WriteFile(env.Emu.Root(), long, []byte("content"))
	env.Start()
	if got := env.Names(""); !slices.Equal(got, []string{long}) {
		t.Fatalf("the root lists %d names, not the %d byte one", len(got), len(long))
	}
	if got := readAll(t, env, long); string(got) != "content" {
		t.Fatalf("the file reads %q", got)
	}
	created := strings.Repeat("ж", 500)
	writeFile(t, env, "", created, []byte("new"))
	uploadAll(t, env)
	if f := inDrive(t, env, created); string(f.Content) != "new" {
		t.Fatalf("Drive has %q under the long name", f.Content)
	}
}

func requireShortenedNamesRead(t *testing.T, env *testenv.Env, stem string, names []string, want map[string]string) {
	t.Helper()
	for _, name := range names {
		if len(name) > core.MaxName || !utf8.ValidString(name) || !strings.HasPrefix(name, stem[:900]) {
			t.Fatalf("a shortened name is %d bytes, valid UTF-8 %v", len(name), utf8.ValidString(name))
		}
		id := item(t, env, name).ID
		if !strings.HasSuffix(name, "ю.txt") && !strings.HasSuffix(name, "ю (1).txt") {
			t.Fatalf("a shortened name ends with %q", name[len(name)-20:])
		}
		if got := readAll(t, env, name); string(got) != want[id] {
			t.Fatalf("a shortened name reads %q", got)
		}
	}
}

func requireWriteUnderShortenedName(t *testing.T, env *testenv.Env, names []string, want map[string]string) {
	t.Helper()

	written := item(t, env, names[1]).ID
	fh := openWriter(t, env, names[1], 0)
	write(t, env, fh, 0, []byte("NEW"))
	env.FS.Release(fh)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(written); !strings.HasPrefix(string(f.Content), "NEW") || len(f.Content) != len(want[written]) {
		t.Fatalf("after a write under a shortened name Drive holds %q", f.Content)
	}
}

func requireShortenedNameTakesChanges(t *testing.T, env *testenv.Env, names []string, want map[string]string) {
	t.Helper()

	waitUntil(t, func() bool {
		return cachedBytes(pieces(t, env, names[0])) == int64(len(want[item(t, env, names[0]).ID]))
	})
	if err := env.FS.Truncate(context.Background(), env.MustResolve(names[0]).Ino, 2); err != nil {
		t.Fatal(err)
	}
	rename(t, env, "", names[1], "", "short.txt")
	uploadAll(t, env)
	first, second := item(t, env, names[0]).ID, item(t, env, "short.txt").ID
	if f, _ := env.Emu.Get(first); string(f.Content) != want[first][:2] {
		t.Fatalf("Drive has %q after the truncate", f.Content)
	}
	if f, _ := env.Emu.Get(second); f.Name != "short.txt" {
		t.Fatalf("Drive has the renamed file as %d bytes of name", len(f.Name))
	}
}

func TestNamesPast1024BytesAreShortened(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	stem := strings.Repeat("ю", 1500)
	a := env.Emu.WriteFile(env.Emu.Root(), stem+"a.txt", []byte("first"))
	b := env.Emu.WriteFile(env.Emu.Root(), stem+"b.txt", []byte("second"))
	env.Start()
	names := env.Names("")
	if len(names) != 2 || names[0] == names[1] {
		t.Fatalf("two long names that share their start list as %d names", len(names))
	}
	want := map[string]string{a: "first", b: "second"}
	requireShortenedNamesRead(t, env, stem, names, want)
	requireWriteUnderShortenedName(t, env, names, want)
	requireShortenedNameTakesChanges(t, env, names, want)
}

func requirePair(t *testing.T, reads map[string]string, plain, a, b string) {
	t.Helper()
	numbered := strings.TrimSuffix(plain, ".txt") + " (1).txt"
	if len(numbered) > core.MaxName {

		for name := range reads {
			if strings.HasSuffix(name, " (1).txt") && strings.HasPrefix(name, plain[:core.MaxName/2]) {
				numbered = name
			}
		}
	}
	got := []string{reads[plain], reads[numbered]}
	slices.Sort(got)
	want := []string{a, b}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the name of %d bytes and its numbered one read %q, want %q", len(plain), got, want)
	}
}

func nameReading(reads map[string]string, content string) (found string) {
	for name, got := range reads {
		if got == content {
			found = name
		}
	}
	return found
}

func TestShortenedNamesKeepEveryItemInSight(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	stem := strings.Repeat("ю", 1500)
	env.Emu.WriteFile(root, stem+".txt", []byte("long"))

	cut := strings.Repeat("ю", (core.MaxName-len(".txt"))/2) + ".txt"
	env.Emu.WriteFile(root, cut, []byte("plain"))

	edge := strings.Repeat("a", core.MaxName-4) + ".txt"
	env.Emu.WriteFile(root, edge, []byte("edge 1"))
	env.Emu.WriteFile(root, edge, []byte("edge 2"))

	env.Emu.WriteFile(root, strings.Repeat("b", 1100)+"v1.2 [final]", []byte("bracket"))
	exact := strings.Repeat("c", core.MaxName)
	env.Emu.WriteFile(root, exact, []byte("exact"))
	env.Start()

	names := env.Names("")
	if len(names) != 6 {
		t.Fatalf("the root lists %d names, want 6", len(names))
	}

	reads := map[string]string{}
	for _, name := range names {
		if _, twice := reads[name]; twice || len(name) > core.MaxName {
			t.Fatalf("a name of %d bytes shows twice or past the limit", len(name))
		}
		reads[name] = string(readAll(t, env, name))
	}

	requirePair(t, reads, cut, "long", "plain")
	requirePair(t, reads, edge, "edge 1", "edge 2")
	if reads[exact] != "exact" {
		t.Fatalf("the name of exactly 1024 bytes reads %q, want its own file", reads[exact])
	}
	if bracket := nameReading(reads, "bracket"); !strings.HasPrefix(bracket, strings.Repeat("b", 1000)) {
		t.Fatalf("the long name with a bracket shows as %d bytes", len(bracket))
	}
}

func TestLongNamesThroughRenameAndCreate(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), strings.Repeat("ю", 1500)+".txt", []byte("long"))
	env.Start()
	root := env.MustResolve("").Ino
	if _, _, err := env.FS.Create(root, strings.Repeat("x", core.MaxName+1),
		syscall.O_WRONLY); err != syscall.ENAMETOOLONG {
		t.Fatalf("a create past the limit got %v, want ENAMETOOLONG", err)
	}
	shown := env.Names("")[0]
	if err := env.FS.Rename(root, shown, root, strings.Repeat("x", core.MaxName+1), 0); err != syscall.ENAMETOOLONG {
		t.Fatalf("a rename past the limit got %v, want ENAMETOOLONG", err)
	}

	mkdir(t, env, "", "sub")
	rename(t, env, "", shown, "sub", shown)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); f.Name != strings.Repeat("ю", 1500)+".txt" {
		t.Fatalf("after the move Drive has a name of %d bytes", len(f.Name))
	}

	newName := strings.Repeat("щ", 500) + ".md"
	rename(t, env, "sub", env.Names("sub")[0], "sub", newName)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); f.Name != newName {
		t.Fatalf("after the rename Drive has a name of %d bytes", len(f.Name))
	}

	readAll(t, env, "sub/"+newName)
	waitUntil(t, func() bool { return cachedBytes(pieces(t, env, "sub/"+newName)) == 4 })
	if err := env.FS.Truncate(context.Background(), env.MustResolve("sub/"+newName).Ino, 1); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); string(f.Content) != "l" {
		t.Fatalf("after the truncate Drive has %q", f.Content)
	}
}
