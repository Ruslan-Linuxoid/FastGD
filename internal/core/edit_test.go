package core_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireRanges(t *testing.T, env *testenv.Env, want ...string) {
	t.Helper()
	got := mediaRanges(env)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("downloads %v, want %v", got, want)
	}
}

func TestPartialWritesFetchOnlyTheirEdgePages(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 1<<20, 90)
	changedUnfetched(t, env)
	fh := openWriter(t, env, "f.bin", 0)
	want := append([]byte(nil), content...)

	patch := testenv.RandomBytes(100, 91)
	write(t, env, fh, 5000, patch)
	copy(want[5000:], patch)
	requireRanges(t, env, "bytes=4096-8191")

	env.Emu.ResetRequests()
	patch = testenv.RandomBytes(10000, 92)
	write(t, env, fh, 10000, patch)
	copy(want[10000:], patch)
	requireRanges(t, env, "bytes=8192-12287", "bytes=16384-20479")

	env.Emu.ResetRequests()
	write(t, env, fh, 6000, []byte("again"))
	copy(want[6000:], "again")
	requireRanges(t, env)

	env.FS.Release(fh)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatal("Drive holds other bytes than the edited file")
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("the edited file reads back different")
	}
}

func TestWholePagesAndAppendsFetchNothing(t *testing.T) {
	t.Parallel()

	env, content := fromDrive(t, 64<<10+100, 93)
	changedUnfetched(t, env)
	fh := openWriter(t, env, "f.bin", 0)
	want := append([]byte(nil), content...)
	pages := testenv.RandomBytes(8192, 94)
	write(t, env, fh, 16384, pages)
	copy(want[16384:], pages)
	write(t, env, fh, int64(len(want)), []byte("appended"))
	want = append(want, "appended"...)
	write(t, env, fh, int64(len(want))+100000, []byte("past a gap"))
	want = append(want, make([]byte, 100000)...)
	want = append(want, "past a gap"...)
	requireRanges(t, env)
	env.FS.Release(fh)

	env.Restart()
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("the appended file reads different")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatal("Drive holds other bytes than the appended file")
	}
}

func TestBytesOfADownloadUnderWayAreHolesInTheWorkingCopy(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 256<<10, 118)

	env.Emu.StallMedia(5000)
	fh := openFile(t, env, "f.bin")
	defer env.FS.Release(fh)
	read := make(chan struct{})
	go func() {
		defer close(read)

		_, _ = env.FS.Read(context.Background(), fh, 0, make([]byte, 64<<10))
	}()

	waitUntil(t, func() bool { return du(t, env) >= 2*4096 })
	changedUnfetched(t, env)
	env.Emu.StallMedia(-1)
	<-read
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file changed here reads other bytes than its Drive version")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("Drive holds other bytes than the file had")
	}
}

func TestHalfWrittenPagesOfABrokenDownloadAreFetchedAgain(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 256<<10, 109)

	env.Emu.CutMedia(-1, 5000)
	fh := openWriter(t, env, "f.bin", 0)
	if _, err := env.FS.Read(context.Background(), fh, 0, make([]byte, 64<<10)); err == nil {
		t.Fatal("a read with every download broken went through")
	}
	env.Emu.CutMedia(0, 0)
	write(t, env, fh, 200000, []byte("edit"))
	want := append([]byte(nil), content...)
	copy(want[200000:], "edit")
	env.FS.Release(fh)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatal("Drive holds other bytes than the edited file")
	}
}

func TestWriteOverAMissingTailKeepsTheWrite(t *testing.T) {
	t.Parallel()

	env, content := fromDrive(t, 10000, 110)
	changedUnfetched(t, env)
	fh := openWriter(t, env, "f.bin", 0)
	patch := testenv.RandomBytes(3808, 111)
	write(t, env, fh, 8192, patch)
	requireRanges(t, env)
	want := append(append([]byte(nil), content[:8192]...), patch...)
	env.FS.Release(fh)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("the file reads the Drive version over the write")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatal("Drive holds other bytes than the written file")
	}
}
