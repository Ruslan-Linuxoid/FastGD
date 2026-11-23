package core_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireDeniedChange(t *testing.T, code int, reason string, undone bool) {
	t.Helper()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "a.txt", []byte("a"))
	env.Start()
	env.Emu.FailChanges("/drive/v3/files/"+id, code, reason)
	env.Emu.ResetRequests()
	rename(t, env, "", "a.txt", "", "b.txt")
	env.Clock.Advance(time.Minute)
	syncOnce(env)
	if n := changes(env); n != 1 {
		t.Fatalf("one round sent %d requests, want one", n)
	}
	names := env.Names("")
	if !undone {
		if len(names) != 1 || names[0] != "b.txt" {
			t.Fatalf("after one answer of a busy Drive the root holds %q, want the new name", names)
		}
		if got := item(t, env, "b.txt").NextTry.Sub(env.Clock.Now()); got != retryWaits()[0] {
			t.Fatalf("the rename waits %v, want %v", got, retryWaits()[0])
		}
		return
	}
	requireChangeUndone(t, env, names)
}

func requireChangeUndone(t *testing.T, env *testenv.Env, names []string) {
	t.Helper()
	if len(names) != 1 || names[0] != "a.txt" {
		t.Fatalf("after the answer the root holds %q, want the name Drive kept", names)
	}
	if it := item(t, env, "a.txt"); it.MetaDirty || it.Attempts != 0 {
		t.Fatalf("the item is left changed=%v with %d failed tries", it.MetaDirty, it.Attempts)
	}
	if what, _, err := env.FS.Store().GetState(meta.StateUndone); err != nil || !strings.Contains(what, "b.txt") {
		t.Fatalf("status tells of the undone change %q: %v", what, err)
	}
	rounds(env, 2)
	if n := changes(env); n != 1 {
		t.Fatalf("%d requests were sent for a change that was undone, want the one", n)
	}
}

func TestDeniedChangeIsUndoneAtOnce(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		code   int
		reason string
		undone bool
	}{
		"403":                {http.StatusForbidden, "insufficientFilePermissions", true},
		"403 of the account": {http.StatusForbidden, "insufficientPermissions", true},
		"404":                {http.StatusNotFound, "notFound", true},
		"409":                {http.StatusConflict, "conflict", true},
		"403 by the rate":    {http.StatusForbidden, "userRateLimitExceeded", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireDeniedChange(t, c.code, c.reason, c.undone)
		})
	}
}

func TestDeniedUploadGoesToLostAndFoundAtOnce(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetUploadFault(func(string) int { return http.StatusForbidden })
	env.Start()
	content := testenv.RandomBytes(3000, 531)
	writeFile(t, env, "", "denied.txt", content)
	env.Clock.Advance(20 * time.Minute)
	syncOnce(env)
	if n := changes(env); n != 1 {
		t.Fatalf("one round sent %d requests, want one", n)
	}
	requireShelved(t, env, "", "denied", ".txt", content)
}

func requireSignInNeededShown(t *testing.T, env *testenv.Env) {
	t.Helper()
	since, needed, err := env.FS.Store().GetState(meta.StateLoginNeeded)
	if err != nil || !needed || since == "" {
		t.Fatalf("status does not tell that a sign-in is needed: %q, %v", since, err)
	}
	if len(env.Logs("signs in again")) != 1 {
		t.Fatalf("the log tells of the sign-in %d times, want once", len(env.Logs("signs in again")))
	}
}

func TestExpiredSignInUndoesNothing(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "mine.txt", []byte("mine"))
	env.Emu.FailRequests(1000000, http.StatusUnauthorized, "authError", 0)
	rounds(env, 12)
	if it := item(t, env, "mine.txt"); !it.Dirty || !strings.Contains(it.LastError, "401") {
		t.Fatalf("without a sign-in the file is dirty %v with the error %q", it.Dirty, it.LastError)
	}
	requireNoLost(t, env)
	if _, needed, _ := env.FS.Store().GetState(meta.StateLoginNeeded); needed {
		t.Fatal("a 401 of Drive alone is told as a refusal of the sign-in")
	}
	env.FS.SignedOut(fmt.Errorf("%w: invalid_grant", drive.ErrLoginExpired))
	requireSignInNeededShown(t, env)

	env.Restart()
	if _, needed, _ := env.FS.Store().GetState(meta.StateLoginNeeded); !needed {
		t.Fatal("after a restart status no longer tells that a sign-in is needed")
	}
	env.Emu.FailRequests(0, 0, "", 0)

	if err := env.FS.PollChanges(context.Background()); err != nil {
		t.Fatal(err)
	}
	syncOnce(env)
	if f := inDrive(t, env, "mine.txt"); string(f.Content) != "mine" {
		t.Fatalf("after the sign-in Drive holds %q", f.Content)
	}
	requireNoLost(t, env)
	if _, needed, _ := env.FS.Store().GetState(meta.StateLoginNeeded); needed {
		t.Fatal("status still tells that a sign-in is needed")
	}
}
