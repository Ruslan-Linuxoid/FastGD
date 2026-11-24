package core_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func retryWaits() []time.Duration {
	return []time.Duration{time.Second, 8 * time.Second, 64 * time.Second, 512 * time.Second, time.Hour}
}

func syncOnce(env *testenv.Env) { env.FS.SyncOnce(context.Background()) }

func changes(env *testenv.Env) int {
	content, metadata := uploads(env)
	return content + metadata
}

func requireBackoffStep(t *testing.T, env *testenv.Env, i int, wait time.Duration) {
	t.Helper()
	it := item(t, env, "busy.txt")
	if got := it.NextTry.Sub(env.Clock.Now()); got != wait || it.Attempts != i+1 || !strings.Contains(it.LastError,
		"503") {
		t.Fatalf("after answer %d the next try is in %v, want %v; %d tries with %q", i+1, got, wait, it.Attempts,
			it.LastError)
	}

	env.Clock.Advance(wait - time.Millisecond)
	syncOnce(env)
	if n := changes(env); n != i+1 {
		t.Fatalf("before the pause %d was over %d requests were sent, want %d", i+1, n, i+1)
	}
	env.Clock.Advance(time.Millisecond)
	syncOnce(env)
	if n := changes(env); n != i+2 {
		t.Fatalf("after the pause %d %d requests were sent, want %d", i+1, n, i+2)
	}
}

func requireUndoneChangeShown(t *testing.T, env *testenv.Env) {
	t.Helper()
	what, _, err := env.FS.Store().GetState(meta.StateUndone)
	if err != nil || !strings.Contains(what, "busy.txt") || !strings.Contains(what, "503") {
		t.Fatalf("status tells of the undone change %q: %v", what, err)
	}
}

func TestBusyAnswersUndoAChangeAfterFiveMoreTries(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetUploadFault(func(name string) int {
		if name == "busy.txt" {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	env.Start()
	content := testenv.RandomBytes(5000, 530)
	writeFile(t, env, "", "busy.txt", content)
	env.Clock.Advance(20 * time.Minute)
	syncOnce(env)
	for i, wait := range retryWaits() {
		requireBackoffStep(t, env, i, wait)
	}
	if names := env.Names(""); len(names) != 0 || !hasLost(env) {
		t.Fatalf("after the sixth answer the root holds %q, want Lost and Found alone", names)
	}
	requireShelved(t, env, "", "busy", ".txt", content)
	if len(env.Logs("busy.txt")) == 0 {
		t.Fatal("the log does not tell of the file Drive did not take")
	}
	requireUndoneChangeShown(t, env)
}

func TestUploadGoesThroughOnceDriveIsNotBusy(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	var answers atomic.Int64
	env.Emu.SetUploadFault(func(name string) int {
		if name == "late.txt" && answers.Add(1) < 5 {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	env.Start()
	writeFile(t, env, "", "late.txt", []byte("late"))
	rounds(env, 4)
	if it := item(t, env, "late.txt"); !it.Dirty || it.Attempts != 4 {
		t.Fatalf("after 4 answers the file is dirty %v with %d tries", it.Dirty, it.Attempts)
	}
	rounds(env, 1)
	if f := inDrive(t, env, "late.txt"); string(f.Content) != "late" {
		t.Fatalf("Drive has %q", f.Content)
	}
	requireNoLost(t, env)
}

func TestOperationsOfOneKindShareThePause(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	var busy atomic.Bool
	busy.Store(true)
	env.Emu.SetUploadFault(func(string) int {
		if busy.Load() {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	env.Start()
	writeFile(t, env, "", "a.txt", []byte("a"))
	writeFile(t, env, "", "b.txt", []byte("b"))
	env.Clock.Advance(20 * time.Minute)
	syncOnce(env)
	for i, wait := range retryWaits()[:2] {
		for _, name := range []string{"a.txt", "b.txt"} {
			if got := item(t, env, name).NextTry.Sub(env.Clock.Now()); got != wait {
				t.Fatalf("after answer %d %s waits %v, want %v as the other", i+1, name, got, wait)
			}
		}
		env.Clock.Advance(wait)
		syncOnce(env)
	}

	busy.Store(false)
	env.Clock.Advance(retryWaits()[2])
	syncOnce(env)
	inDrive(t, env, "a.txt")
	inDrive(t, env, "b.txt")
	busy.Store(true)
	writeFile(t, env, "", "c.txt", []byte("c"))
	env.Clock.Advance(20 * time.Minute)
	syncOnce(env)
	if got := item(t, env, "c.txt").NextTry.Sub(env.Clock.Now()); got != retryWaits()[0] {
		t.Fatalf("after an upload went through the next busy answer waits %v, want %v", got, retryWaits()[0])
	}

	busy.Store(false)
	env.Emu.FailChanges("/drive/v3/files/"+item(t, env, "a.txt").ID, http.StatusServiceUnavailable, "backendError")
	rename(t, env, "", "a.txt", "", "renamed.txt")
	env.Clock.Advance(retryWaits()[0])
	syncOnce(env)
	inDrive(t, env, "c.txt")
	if got := item(t, env, "renamed.txt").NextTry.Sub(env.Clock.Now()); got != retryWaits()[0] {
		t.Fatalf("the rename waits %v after its first busy answer, want %v", got, retryWaits()[0])
	}
}

func TestPreconditionGoesAgainOnceAndThenAsABusyAnswer(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "a.txt", []byte("a"))
	env.Start()
	env.Emu.FailChanges("/drive/v3/files/"+id, http.StatusPreconditionFailed, "conditionNotMet")
	env.Emu.ResetRequests()
	rename(t, env, "", "a.txt", "", "b.txt")
	env.Clock.Advance(time.Minute)
	syncOnce(env)
	if it := item(t, env, "b.txt"); changes(env) != 1 || it.NextTry.After(env.Clock.Now()) {
		t.Fatalf("after the first 412: %d requests, the next try at %v, want one and at once", changes(env), it.NextTry)
	}

	syncOnce(env)
	for i, wait := range retryWaits() {
		if got := item(t, env, "b.txt").NextTry.Sub(env.Clock.Now()); got != wait || changes(env) != i+2 {
			t.Fatalf("after %d requests the next try is in %v, want %v after %d", changes(env), got, wait, i+2)
		}
		env.Clock.Advance(wait)
		syncOnce(env)
	}
	if n := changes(env); n != 7 {
		t.Fatalf("%d requests were sent, want the first, the one at once and five more", n)
	}
	if names := env.Names(""); len(names) != 1 || names[0] != "a.txt" {
		t.Fatalf("after the last answer the root holds %q, want the name Drive kept", names)
	}
}

func TestFiftiethBusyAnswerOverTheRunsUndoesAChange(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetUploadFault(func(string) int { return http.StatusServiceUnavailable })
	env.Start()
	content := testenv.RandomBytes(2000, 532)
	writeFile(t, env, "", "busy.txt", content)
	answers := 0
	for run := range 10 {

		for range 5 {
			if names := env.Names(""); len(names) != 1 || names[0] != "busy.txt" {
				t.Fatalf("after %d answers, in run %d, the root holds %q", answers, run, names)
			}
			env.Clock.Advance(2 * time.Hour)
			syncOnce(env)
			answers++
		}
		if run < 9 {
			env.Restart()
		}
	}
	if answers != 50 {
		t.Fatalf("scenario not built: %d answers", answers)
	}
	requireShelved(t, env, "", "busy", ".txt", content)
}
