package core_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func fetchRounds(env *testenv.Env, n int) {
	for range n {
		env.Clock.Advance(time.Hour)
		env.FS.FillBases(context.Background())
	}
}

func shelvedNames(env *testenv.Env) []string {
	if _, err := env.Resolve(lostDir); err != nil {
		return nil
	}
	return lostNames(env, lostDir)
}

func requireGivenUp(t *testing.T, env *testenv.Env, inDrive []byte, off int64, local []byte) {
	t.Helper()
	names := lostNames(env, lostDir)
	if len(names) != 1 || !conflictName("f", ".bin").MatchString(names[0]) {
		t.Fatalf("Lost and Found holds %q, want the local version of f.bin", names)
	}
	if note := requireNote(t, env, lostDir, names[0]); !strings.Contains(note, "в этой копии пустые") {
		t.Fatalf("the note does not tell of the parts left empty: %q", note)
	}
	got := readAll(t, env, lostDir+"/"+names[0])
	if !bytes.Equal(got[off:off+4096], local[off:off+4096]) {
		t.Fatal("the copy in Lost and Found lacks the edit")
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, inDrive) {
		t.Fatal("f.bin does not hold Drive's version")
	}
	if it := item(t, env, "f.bin"); it.Dirty {
		t.Fatal("f.bin stays changed here")
	}
	if st := readStatus(t, env); st.Undone.IsZero() {
		t.Fatal("status does not tell of the edit undone")
	}
}

func TestBaseFetchFailingNineTimesMoreGivesUp(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	local := editedFromDrive(t, env, 256<<10, 8192, 1370)
	inDrive := inDrive(t, env, "f.bin").Content
	env.Emu.FailMedia(http.StatusServiceUnavailable, "backendError")
	fetchRounds(env, 9)
	if it := item(t, env, "f.bin"); !it.Dirty || len(shelvedNames(env)) != 0 {
		t.Fatalf("after nine failed tries the file is changed here %v, with Lost and Found holding %q", it.Dirty,
			shelvedNames(env))
	}
	fetchRounds(env, 1)
	env.Emu.FailMedia(0, "")
	requireGivenUp(t, env, inDrive, 8192, local)
}

func TestBaseFetchRefusedGivesUpAtOnce(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	local := editedFromDrive(t, env, 256<<10, 8192, 1371)
	inDrive := inDrive(t, env, "f.bin").Content
	env.Emu.FailMedia(http.StatusForbidden, "cannotDownloadFile")
	fetchRounds(env, 1)
	env.Emu.FailMedia(0, "")
	requireGivenUp(t, env, inDrive, 8192, local)
}

func TestBaseFetchWaitsForTheSignInAndTheNetwork(t *testing.T) {
	t.Parallel()
	for name, cut := range map[string]func(env *testenv.Env, on bool){
		"sign-in": func(env *testenv.Env, on bool) {
			if on {
				env.Emu.FailMedia(http.StatusUnauthorized, "authError")
			} else {
				env.Emu.FailMedia(0, "")
			}
		},
		"network": func(env *testenv.Env, on bool) { env.Emu.SetOffline(on) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			local := editedFromDrive(t, env, 256<<10, 8192, 1372)
			cut(env, true)
			fetchRounds(env, 20)
			if it := item(t, env, "f.bin"); !it.Dirty || len(shelvedNames(env)) != 0 {
				t.Fatalf("waiting, the file is changed here %v, with Lost and Found holding %q", it.Dirty, shelvedNames(env))
			}
			cut(env, false)

			env.Emu.FailMedia(http.StatusServiceUnavailable, "backendError")
			fetchRounds(env, 1)
			env.Emu.FailMedia(0, "")
			if it := item(t, env, "f.bin"); !it.Dirty || len(shelvedNames(env)) != 0 {
				t.Fatalf("one failure after the waits: the file is changed here %v, with Lost and Found holding %q", it.Dirty,
					shelvedNames(env))
			}
			env.Emu.ResetRequests()
			fetchRounds(env, 1)
			if mediaGets(env) == 0 {
				t.Fatal("once back, the background fetch of the base does not go")
			}
			uploadAll(t, env)
			if !bytes.Equal(inDrive(t, env, "f.bin").Content, local) {
				t.Fatal("the edit did not reach Drive on its whole base")
			}
		})
	}
}

func TestBaseFetchPastTheDownloadQuotaBacksOff(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	local := editedFromDrive(t, env, 256<<10, 8192, 1510)
	env.Emu.SetDownloadQuotaExceeded(true)
	for range 360 {
		env.Clock.Advance(10 * time.Second)
		env.FS.FillBases(context.Background())
	}
	if n := mediaGets(env); n == 0 || n > 15 {
		t.Fatalf("in an hour past the quota the fetch asked Drive for content %d times, want a few", n)
	}
	if it := item(t, env, "f.bin"); !it.Dirty || len(shelvedNames(env)) != 0 {
		t.Fatalf("past the quota the file is changed here %v, with Lost and Found holding %q", it.Dirty, shelvedNames(env))
	}
	env.Emu.SetDownloadQuotaExceeded(false)
	fetchRounds(env, 1)
	uploadAll(t, env)
	if !bytes.Equal(inDrive(t, env, "f.bin").Content, local) {
		t.Fatal("the edit did not reach Drive on its whole base")
	}
}

func TestBaseGivenUpMoveFailingKeepsTheBase(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	var broken atomic.Bool
	env.Tune = func(o *core.Options) {
		o.ShelveFault = func(at string) error {
			if at == core.ShelveSteps()[1] && broken.Load() {
				return errors.New("the cache broke")
			}
			return nil
		}
	}
	local := editedFromDrive(t, env, 256<<10, 8192, 1540)
	broken.Store(true)
	env.Emu.FailMedia(http.StatusForbidden, "cannotDownloadFile")
	fetchRounds(env, 1)
	if it := item(t, env, "f.bin"); !it.Dirty || it.Base == 0 || len(shelvedNames(env)) != 0 {
		t.Fatalf("after the failed move the file is changed here %v with base %d, and Lost and Found holds %q", it.Dirty,
			it.Base, shelvedNames(env))
	}
	broken.Store(false)
	env.Emu.FailMedia(0, "")
	uploadAll(t, env)
	if !bytes.Equal(inDrive(t, env, "f.bin").Content, local) {
		t.Fatal("the edit did not reach Drive on its whole base")
	}
}

func TestBaseGivenUpWhileDriveDoesNotAnswerMovesLater(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	local := editedFromDrive(t, env, 256<<10, 8192, 1541)
	f := inDrive(t, env, "f.bin")
	env.Emu.FailPath("/drive/v3/files/"+f.ID, http.StatusServiceUnavailable, "backendError")
	fetchRounds(env, 10)
	if it := item(t, env, "f.bin"); !it.Dirty || it.Base == 0 || len(shelvedNames(env)) != 0 {
		t.Fatalf("with Drive silent the file is changed here %v with base %d, and Lost and Found holds %q", it.Dirty, it.Base,
			shelvedNames(env))
	}
	if st := readStatus(t, env); !st.Undone.IsZero() {
		t.Fatal("status tells of an edit undone")
	}
	env.Emu.FailPath("", 0, "")
	env.Emu.FailMedia(http.StatusServiceUnavailable, "backendError")
	fetchRounds(env, 1)
	env.Emu.FailMedia(0, "")
	requireGivenUp(t, env, f.Content, 8192, local)
}
