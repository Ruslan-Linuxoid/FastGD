package core_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func flagged(t *testing.T, notMine bool) (*testenv.Env, string, []byte) {
	t.Helper()
	env := testenv.New(t)
	content := testenv.RandomBytes(300<<10, 1360)
	id := env.Emu.Put(emulator.File{Name: "f.bin", MimeType: "application/octet-stream", Parents: []string{env.Emu.Root()},
		Content: content, Abusive: true, NotMine: notMine})
	env.Start()
	env.Emu.ResetRequests()
	return env, id, content
}

func TestEmulatorGivesAFlaggedFileToItsOwnerAlone(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		notMine, ack bool
		want         int
	}{
		"owner, no acknowledgement":   {false, false, http.StatusForbidden},
		"owner, acknowledgement":      {false, true, http.StatusOK},
		"not the owner, acknowledged": {true, true, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env, id, _ := flagged(t, c.notMine)
			url := env.Srv.URL + "/drive/v3/files/" + id + "?alt=media"
			if c.ack {
				url += "&acknowledgeAbuse=true"
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var body bytes.Buffer
			if _, err := body.ReadFrom(res.Body); err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != c.want {
				t.Fatalf("the download answered %d, want %d", res.StatusCode, c.want)
			}
			if c.want == http.StatusForbidden && !strings.Contains(body.String(), "cannotDownloadAbusiveFile") {
				t.Fatalf("the refusal is %s", body.String())
			}
		})
	}
}

func TestFlaggedFileOfTheAccountReadsAndTakesEdits(t *testing.T) {
	t.Parallel()
	env, _, content := flagged(t, false)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the flagged file reads other bytes")
	}
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 100, []byte("edit"))
	env.FS.Release(fh)
	uploadAll(t, env)
	want := bytes.Clone(content)
	copy(want[100:], "edit")
	if !bytes.Equal(inDrive(t, env, "f.bin").Content, want) {
		t.Fatal("the edit of the flagged file did not reach Drive")
	}
}

func TestFlaggedFileOfAnotherGetsNoAccess(t *testing.T) {
	t.Parallel()
	env, id, _ := flagged(t, true)
	ino := env.MustResolve("f.bin").Ino
	fh, err := env.FS.Open(context.Background(), ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.FS.Read(context.Background(), fh, 0, make([]byte, 4096))
	env.FS.Release(fh)
	testenv.RequireErrno(t, err, syscall.EACCES)

	atRest(t, env)
	env.Emu.ResetRequests()
	_, err = env.FS.Open(context.Background(), ino, syscall.O_RDONLY)
	testenv.RequireErrno(t, err, syscall.EACCES)
	if n := len(env.Emu.Requests()); n != 0 {
		t.Fatalf("the open after the refusal went to Drive %d times", n)
	}
	st := readStatus(t, env)
	if st.AbuseRefused.IsZero() || st.AbusivePath != "My Drive/f.bin" {
		t.Fatalf("status tells the refusal at %v of %q", st.AbuseRefused, st.AbusivePath)
	}

	env.Emu.Update(id, func(f *emulator.File) { f.Abusive = false })
	poll(t, env)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, inDrive(t, env, "f.bin").Content) {
		t.Fatal("the file Drive no longer flags reads other bytes")
	}

	atRest(t, env)
	if st := readStatus(t, env); !st.AbuseRefused.IsZero() {
		t.Fatalf("status still tells the refusal of %q", st.AbusivePath)
	}
}
