package core_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func listCopy(t *testing.T, aside string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(aside, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		fmt.Fprintf(&b, "%s %d\n", strings.TrimPrefix(p, aside), fi.Size())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

type copyMarks struct {
	env  *testenv.Env
	held map[string]string
}

func (m *copyMarks) mark(t *testing.T, n int, upload bool) {
	t.Helper()
	env := m.env
	name := fmt.Sprintf("made-%d.bin", n)
	content := testenv.RandomBytes(3000, uint64(620+n))
	env.Emu.SetOffline(true)
	writeFile(t, env, "", name, content)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	if err := os.WriteFile(filepath.Join(env.Dir, "untrusted"), fmt.Appendf(nil, "mark %d\n", n), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Emu.SetOffline(false)
	env.Start()
	latest := m.requireOwnCopy(t, n)
	what, _, err := env.FS.Store().GetState(meta.StateDiscarded)
	if err != nil || !strings.Contains(what, latest) || !strings.Contains(what, fmt.Sprintf("mark %d", n)) {
		t.Fatalf("status tells of the state set aside %q, want the copy %s and its reason: %v", what, latest, err)
	}
	if len(env.Logs(fmt.Sprintf("copies=%d", n))) == 0 {
		t.Fatalf("the log does not tell that %d copies lie", n)
	}
	if got := readAll(t, env, name); !bytes.Equal(got, content) {
		t.Fatalf("the file made before mark %d is not back in the mount as it was", n)
	}
	if upload {
		uploadAll(t, env)
		if f := inDrive(t, env, name); !bytes.Equal(f.Content, content) {
			t.Fatalf("the file made before mark %d went to Drive as %d bytes", n, len(f.Content))
		}
	}

	m.held[latest] = listCopy(t, latest)
}

func (m *copyMarks) requireOwnCopy(t *testing.T, n int) string {
	t.Helper()
	copies := stateCopies(t, m.env)
	if marked(m.env) || len(copies) != n {
		t.Fatalf("after mark %d: marked %v, copies %q", n, marked(m.env), copies)
	}
	for aside, was := range m.held {
		if now := listCopy(t, aside); now != was {
			t.Fatalf("mark %d changed the earlier copy %s:\n%s\nwas\n%s", n, aside, now, was)
		}
	}
	latest := copies[len(copies)-1]
	if _, seen := m.held[latest]; seen {
		t.Fatalf("mark %d went into the copy %s of an earlier one", n, latest)
	}
	return latest
}

func TestEveryMarkSetsTheStateAsideIntoACopyOfItsOwn(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	m := &copyMarks{env: env, held: map[string]string{}}
	for n := 1; n <= 3; n++ {
		m.mark(t, n, true)
	}

	m.mark(t, 4, false)
	m.mark(t, 5, false)
	uploadAll(t, env)
	for n := 4; n <= 5; n++ {
		inDrive(t, env, fmt.Sprintf("made-%d.bin", n))
	}
}
