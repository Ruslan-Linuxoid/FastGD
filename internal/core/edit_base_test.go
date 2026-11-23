package core_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestWriteIntoAFileChangedHereDoesNotWaitForItsBase(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		content := startWith(env, 2<<20, 97)
		changedUnfetched(t, env)

		env.Emu.SetMediaDelay(time.Second)
		fh := openWriter(t, env, "f.bin", 0)
		start := time.Now()
		write(t, env, fh, 1<<20, testenv.RandomBytes(64<<10, 98))
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Fatalf("a write of whole pages took %v", took)
		}
		want := append([]byte(nil), content...)
		copy(want[1<<20:], testenv.RandomBytes(64<<10, 98))
		env.FS.Release(fh)
		env.Emu.SetMediaDelay(0)
		uploadAll(t, env)
		if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
			t.Fatal("Drive holds other bytes than the edited file")
		}
	})
}

func TestReadOfAnEditedFileFetchesItsBase(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 256<<10, 105)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 100000, []byte("edit"))
	want := append([]byte(nil), content...)
	copy(want[100000:], "edit")
	if got := mustRead(t, env, fh, 90000, 20000); !bytes.Equal(got, want[90000:110000]) {
		t.Fatal("a read around the edit differs")
	}
	env.FS.Release(fh)

	env.Restart()
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("after a restart the edited file reads different")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatal("Drive holds other bytes than the edited file")
	}
}

func TestFirstWriteAfterAChangeInDriveBuildsOnTheNewVersion(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, 64<<10, 115)

	fh := openWriter(t, env, "f.bin", 0)
	id := item(t, env, "f.bin").ID
	changed := testenv.RandomBytes(100<<10, 116)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = changed })
	poll(t, env)
	write(t, env, fh, 50000, []byte("edit"))
	want := append([]byte(nil), changed...)
	copy(want[50000:], "edit")
	env.FS.Release(fh)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatalf("Drive holds %d bytes, want the new version of %d with the edit", len(f.Content), len(want))
	}
}

func TestAFetchForAWriteHoldsUpNoOtherFile(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		startWith(env, 64<<10, 117)
		writeFile(t, env, "", "other.txt", []byte("other"))
		env.Emu.SetMediaDelay(time.Second)
		fh := openWriter(t, env, "f.bin", 0)
		wrote := make(chan error)
		go func() {
			_, err := env.FS.Write(context.Background(), fh, 100, []byte("needs its page"))
			wrote <- err
		}()
		waitUntil(t, func() bool { return len(mediaRanges(env)) > 0 })
		start := time.Now()
		env.FS.SyncOnce(context.Background())
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Fatalf("uploads waited %v for a write's fetch", took)
		}
		if err := <-wrote; err != nil {
			t.Fatal(err)
		}
		env.FS.Release(fh)
	})
}

func TestFirstWriteOvertakenByAPollBuildsOnOneVersion(t *testing.T) {
	t.Parallel()
	for name, size := range map[string]int{"longer": 200 << 10, "shorter": 40 << 10} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env, _ := fromDrive(t, 100<<10, 120)
			fh := openWriter(t, env, "f.bin", 0)
			id := item(t, env, "f.bin").ID
			changed := testenv.RandomBytes(size, 121)

			env.Emu.SetMediaDelay(time.Second)
			wrote := make(chan error)
			go func() {
				_, err := env.FS.Write(context.Background(), fh, 10000, []byte("edit"))
				wrote <- err
			}()
			waitUntil(t, func() bool { return len(mediaRanges(env)) > 0 })
			env.Emu.Update(id, func(f *emulator.File) { f.Content = changed })
			poll(t, env)
			env.Emu.SetMediaDelay(0)
			if err := <-wrote; err != nil {
				t.Fatal(err)
			}
			env.FS.Release(fh)
			want := append([]byte(nil), changed...)
			copy(want[10000:], "edit")
			if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
				t.Fatalf("the file reads %d bytes, want the new version of %d with the edit", len(got), len(want))
			}
			uploadAll(t, env)
			if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
				t.Fatalf("Drive holds %d bytes, want the new version of %d with the edit", len(f.Content), len(want))
			}
		})
	}
}
