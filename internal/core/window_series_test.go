package core_test

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestFirstReadFetchesItAlone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(big, 3)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	fh := openFile(t, env, "f.bin")

	env.Emu.ResetRequests()
	off := int64(mib + 8*kib)
	if got := mustRead(t, env, fh, off, 4*kib); !bytes.Equal(got, content[off:off+4*kib]) {
		t.Fatal("bytes differ from Drive's")
	}
	if got := settled(t, env, 1); !slices.Equal(got, []string{rangeHeader(off, off+4*kib)}) {
		t.Fatalf("a first read downloads %q, want its own bytes alone", got)
	}

	for _, at := range []int64{3 * mib, 64 * kib, 2 * mib} {
		mustRead(t, env, fh, at, 4*kib)
	}
	if got := settled(t, env, 4); len(got) != 4 {
		t.Fatalf("four reads at random download %q", got)
	}

	env.Emu.ResetRequests()
	again := openFile(t, env, "f.bin")
	mustRead(t, env, again, off-4*kib, 12*kib)
	got := settled(t, env, 2)
	if !slices.Contains(got, rangeHeader(off-4*kib, off)) || slices.Contains(got, rangeHeader(off, off+4*kib)) {
		t.Fatalf("reading around a piece downloads %q", got)
	}
}

func requireWindowGrows(t *testing.T, env *testenv.Env, chunk, chunks int64) {
	t.Helper()
	sizes := requestSizes(t, env)
	if len(sizes) < 3 || sizes[0] != chunk || sizes[1] != chunk {
		t.Fatalf("a sequential read starts with requests of %v", sizes)
	}
	for i := 2; i < len(sizes)-1; i++ {
		if sizes[i] < sizes[i-1] && sizes[i] != slices.Max(sizes[:i]) {
			t.Fatalf("the window went from %d to %d: %v", sizes[i-1], sizes[i], sizes)
		}
	}
	if slices.Max(sizes) < 4*mib || len(sizes) > 14 {
		t.Fatalf("a sequential read of %d chunks made requests of %v", chunks, sizes)
	}
}

func TestSequentialReadGrowsTheWindowWithTheSeries(t *testing.T) {
	env := testenv.New(t)
	const size = 16 * mib
	content := testenv.RandomBytes(size, 4)
	env.Emu.WriteFile(env.Emu.Root(), "video.bin", content)
	env.Start()

	env.Emu.SetMediaRate(32 * mib)
	fh := openFile(t, env, "video.bin")

	env.Emu.ResetRequests()
	const chunk = 64 * kib
	var got []byte
	for off := int64(0); off < size; off += chunk {
		got = append(got, mustRead(t, env, fh, off, chunk)...)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("the sequential read differs from Drive's")
	}
	requireWindowGrows(t, env, chunk, size/chunk)

	env.Emu.ResetRequests()
	other := testenv.RandomBytes(big, 5)
	env.Emu.WriteFile(env.Emu.Root(), "other.bin", other)
	poll(t, env)
	fo := openFile(t, env, "other.bin")
	mustRead(t, env, fo, 0, 64*kib)
	mustRead(t, env, fo, 64*kib, 64*kib)
	mustRead(t, env, fo, 3*mib, 8*kib)
	got2 := settled(t, env, 4)
	if !slices.Contains(got2, rangeHeader(3*mib, 3*mib+8*kib)) || len(got2) != 4 {
		t.Fatalf("after a jump the reads download %q, want the jump's bytes alone", got2)
	}
}

func TestSeriesStartsAgainAfterAClose(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(big, 6))
	env.Start()
	fh := openFile(t, env, "f.bin")
	mustRead(t, env, fh, 0, 64*kib)
	env.FS.Release(fh)
	env.Emu.ResetRequests()
	again := openFile(t, env, "f.bin")
	mustRead(t, env, again, 64*kib, 64*kib)
	if got := settled(t, env, 1); !slices.Equal(got, []string{rangeHeader(64*kib, 128*kib)}) {
		t.Fatalf("the first read of a new open downloads %q, want its own bytes alone", got)
	}
}

func TestSeriesBackwardFetchesBeforeTheRead(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(big, 7))
	env.Start()
	fh := openFile(t, env, "f.bin")
	env.Emu.ResetRequests()
	for _, off := range []int64{3 * 64 * kib, 2 * 64 * kib} {
		mustRead(t, env, fh, off, 64*kib)
	}
	if got := settled(t, env, 3); !slices.Contains(got, rangeHeader(64*kib, 128*kib)) {
		t.Fatalf("a series backward downloads %q, want the bytes before the read", got)
	}
}

func TestChangedFileFetchesWhatIsReadAlone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	editedFromDrive(t, env, 4*mib, 3*mib, 8)
	fh := openFile(t, env, "f.bin")
	for off := int64(0); off < 4*64*kib; off += 64 * kib {
		mustRead(t, env, fh, off, 64*kib)
	}
	for _, r := range mediaRanges(env) {
		var from, to int64
		if _, err := fmt.Sscanf(r, "bytes=%d-%d", &from, &to); err != nil || to >= 4*64*kib {
			t.Fatalf("a series of reads of a changed file downloads %s", r)
		}
	}
}
