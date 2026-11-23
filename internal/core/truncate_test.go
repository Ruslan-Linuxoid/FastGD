package core_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

type step struct {
	data []byte
	off  int64
	size int64
}

func requireTruncationMatchesExt4(t *testing.T, steps []step) {
	t.Helper()
	env, content := fromDrive(t, 300<<10, 96)

	ref := filepath.Join(t.TempDir(), "ref")
	if err := os.WriteFile(ref, content, 0o600); err != nil {
		t.Fatal(err)
	}
	rf, err := os.OpenFile(ref, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	fh := openWriter(t, env, "f.bin", 0)
	applySteps(t, env, fh, rf, steps)
	env.FS.Release(fh)
	want, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("the file reads different from the same steps on the local file system")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatal("Drive holds other bytes than the file")
	}
}

func applySteps(t *testing.T, env *testenv.Env, fh uint64, rf *os.File, steps []step) {
	t.Helper()
	for _, s := range steps {
		if s.data != nil {
			write(t, env, fh, s.off, s.data)
			if _, err := rf.WriteAt(s.data, s.off); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := env.FS.Truncate(context.Background(), env.MustResolve("f.bin").Ino, s.size); err != nil {
			t.Fatal(err)
		}
		if err := rf.Truncate(s.size); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTruncationMatchesExt4AndReachesDrive(t *testing.T) {
	t.Parallel()
	for name, steps := range map[string][]step{

		"cut then write": {{size: 70000}, {data: []byte("into the cut page"), off: 69990}, {size: 200000},
			{data: []byte("past the cut"), off: 150000}},

		"cut then grow": {{size: 5000}, {size: 9000}},

		"grow then write": {{size: 400000}, {data: []byte("in the grown part"), off: 350000}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireTruncationMatchesExt4(t, steps)
		})
	}
}
