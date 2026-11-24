package core_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func repeatsTold(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if m := repeatsField.FindStringSubmatch(l); m != nil {
			out[i] = m[1]
		}
	}
	return out
}

var repeatsField = regexp.MustCompile(`repeats=(\d+)`)

const loopers = 8

func refusalLines(env *testenv.Env, id string) []string {
	var out []string
	for _, l := range env.Logs("Drive does not let the user change the file") {
		if strings.Contains(l, id) {
			out = append(out, l)
		}
	}
	return out
}

func TestRefusalsInALoopTakeALineInTenMinutes(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	locked := foreign(env, "locked.txt", func(f *emulator.File) { f.CannotModify = true })
	other := foreign(env, "other.txt", func(f *emulator.File) { f.CannotModify = true })
	env.Start()
	inos := map[string]uint64{}
	for _, name := range []string{"locked.txt", "other.txt"} {
		inos[name] = env.MustResolve(name).Ino
	}
	open := func(name string) {
		_, err := env.FS.Open(context.Background(), inos[name], uint32(syscall.O_WRONLY))
		if !errors.Is(err, syscall.EACCES) {
			t.Errorf("an open of %s to write: %v, want EACCES", name, err)
		}
	}
	var loops sync.WaitGroup
	for range loopers {
		loops.Go(func() {
			for range 10000 / loopers {
				open("locked.txt")
				env.Clock.Advance(6 * time.Millisecond)
			}
		})
	}
	loops.Wait()
	open("other.txt")
	if got := refusalLines(env, other); len(got) != 1 {
		t.Fatalf("a refusal of another file takes %d lines, want 1", len(got))
	}
	for range 2 {
		env.Clock.Advance(10 * time.Minute)
		open("locked.txt")
	}
	if got := repeatsTold(refusalLines(env, locked)); !slices.Equal(got, []string{"", "9999", ""}) {
		t.Fatalf("the lines of the refusals of the file tell the repeats %q, want none, 9999, none", got)
	}
}
