//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const (
	seededCacheLimit = 8 << 20

	seededRounds = 8
)

type seededRun struct {
	seed    uint64
	rng     *rand.Rand
	e       *emu
	d       *daemon
	files   map[string]*crashFile
	names   []string
	created int
}

func newSeededRun(t *testing.T) *seededRun {
	t.Helper()
	seed := uint64(time.Now().UnixNano())
	if v := os.Getenv(crashSeedEnv); v != "" {
		var err error
		if seed, err = strconv.ParseUint(v, 10, 64); err != nil {
			t.Fatalf("%s: %v", crashSeedEnv, err)
		}
	}
	t.Logf("seed %d: run with %s=%d to repeat the sequence", seed, crashSeedEnv, seed)
	r := &seededRun{seed: seed, rng: rand.New(rand.NewPCG(seed, 15)), e: startEmu(t), files: map[string]*crashFile{}}
	clean := r.e.Mkdir(r.e.Root(), "clean")
	r.e.Mkdir(r.e.Root(), "made")
	for i := range 12 {
		name := fmt.Sprintf("clean/c%02d.bin", i)
		content := testenv.RandomBytes(1<<20+r.rng.IntN(2<<20), seed+uint64(i))
		r.e.WriteFile(clean, filepath.Base(name), content)
		r.files[name] = &crashFile{want: content}
		r.names = append(r.names, name)
	}
	r.d = newDaemon(t, r.e)
	r.d.cacheLimit = seededCacheLimit
	r.d.start()
	return r
}

func (r *seededRun) check(t *testing.T, name string) {
	t.Helper()
	f := r.files[name]
	got, err := os.ReadFile(r.d.path(name))
	if f.maybe == nil {
		if err != nil || !bytes.Equal(got, f.want) {
			t.Fatalf("seed %d: %s does not read as written: %v, %d bytes of %d", r.seed, name, err, len(got), len(f.want))
		}
		return
	}
	if os.IsNotExist(err) && f.made && f.want == nil {
		delete(r.files, name)
		return
	}
	if err != nil {
		t.Fatalf("seed %d: %s, whose write a kill cut: %v", r.seed, name, err)
	}
	r.requireBetweenTheContents(t, name, f, got)
	f.want, f.maybe = got, nil
}

func (r *seededRun) requireBetweenTheContents(t *testing.T, name string, f *crashFile, got []byte) {
	t.Helper()
	if len(got) < min(len(f.want), len(f.maybe)) || len(got) > max(len(f.want), len(f.maybe)) {
		t.Fatalf("seed %d: %s has %d bytes after a write cut by a kill, between %d and %d were written", r.seed, name,
			len(got), len(f.want), len(f.maybe))
	}
	for i, b := range got {
		if (i >= len(f.want) || f.want[i] != b) && (i >= len(f.maybe) || f.maybe[i] != b) {
			t.Fatalf("seed %d: %s holds at %d a byte that was never written there", r.seed, name, i)
		}
	}
}

func (r *seededRun) write(name string, next []byte, fn func(fd *os.File) error) error {
	sync := r.rng.IntN(2) == 0
	f := r.files[name]
	if f == nil {
		f = &crashFile{made: true}
		r.files[name] = f
		r.names = append(r.names, name)
	}
	fd, err := os.OpenFile(r.d.path(name), os.O_RDWR|os.O_CREATE, 0o644)
	if err == nil {
		err = fn(fd)
		if err == nil && sync {
			err = fd.Sync()
		}
		if cerr := fd.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		f.maybe = next
		return err
	}
	f.want = next
	return nil
}

func (r *seededRun) step() (wrong string, err error) {
	name := r.names[r.rng.IntN(len(r.names))]

	for r.files[name] == nil {
		name = r.names[r.rng.IntN(len(r.names))]
	}
	f := r.files[name]
	switch op := r.rng.IntN(20); {
	case op < 4:
		return "", r.createFile()
	case op < 9:
		return "", r.patchFile(name, f)
	case op < 11:
		return "", r.appendToFile(name, f)
	case op < 13:
		return r.readWhole(name, f)
	case op < 19:
		return r.readPart(name, f)
	default:

		time.Sleep(time.Duration(1+r.rng.IntN(4)) * time.Second)
		return "", nil
	}
}

func (r *seededRun) createFile() error {
	r.created++
	content := testenv.RandomBytes(4<<10+r.rng.IntN(1<<20), r.seed+1_000_000+uint64(r.created))
	return r.write(fmt.Sprintf("made/m%03d.bin", r.created), content, func(fd *os.File) error {
		_, err := fd.Write(content)
		return err
	})
}

func (r *seededRun) patchFile(name string, f *crashFile) error {
	patch := testenv.RandomBytes(1+r.rng.IntN(64<<10), r.rng.Uint64())
	off := r.rng.IntN(len(f.want) + 1)
	next := append([]byte(nil), f.want...)
	if end := off + len(patch); end > len(next) {
		next = append(next, make([]byte, end-len(next))...)
	}
	copy(next[off:], patch)
	return r.write(name, next, func(fd *os.File) error {
		_, err := fd.WriteAt(patch, int64(off))
		return err
	})
}

func (r *seededRun) appendToFile(name string, f *crashFile) error {
	tail := testenv.RandomBytes(1+r.rng.IntN(128<<10), r.rng.Uint64())
	next := append(append([]byte(nil), f.want...), tail...)
	return r.write(name, next, func(fd *os.File) error {
		_, err := fd.WriteAt(tail, int64(len(f.want)))
		return err
	})
}

func (r *seededRun) readWhole(name string, f *crashFile) (wrong string, err error) {
	got, err := os.ReadFile(r.d.path(name))
	if err == nil && !bytes.Equal(got, f.want) {
		wrong = fmt.Sprintf("%s reads different from what was written, %d bytes of %d", name, len(got), len(f.want))
	}
	return wrong, err
}

func (r *seededRun) readPart(name string, f *crashFile) (wrong string, err error) {
	if len(f.want) == 0 {
		return "", nil
	}
	off := r.rng.IntN(len(f.want))
	n := 1 + r.rng.IntN(min(len(f.want)-off, 256<<10))
	fd, err := os.Open(r.d.path(name))
	if err != nil {
		return "", err
	}
	defer fd.Close()
	got := make([]byte, n)
	if _, err := fd.ReadAt(got, int64(off)); err != nil {
		return "", err
	}
	if !bytes.Equal(got, f.want[off:off+n]) {
		wrong = fmt.Sprintf("%s reads different at %d+%d from what was written", name, off, n)
	}
	return wrong, nil
}

func (r *seededRun) round(t *testing.T, round int) (inDrive int) {
	t.Helper()

	whole := 3 + r.rng.IntN(40)
	moment := time.Duration(r.rng.IntN(30_000)) * time.Microsecond
	for range whole {
		if wrong, err := r.step(); wrong != "" || err != nil {
			t.Fatalf("seed %d: round %d, with the daemon up: %s %v", r.seed, round, wrong, err)
		}
	}
	killed := make(chan struct{})
	go func() {
		time.Sleep(moment)
		r.d.kill()
		close(killed)
	}()
	wrong, _ := r.step()
	<-killed
	if wrong != "" {
		t.Fatalf("seed %d: round %d: %s", r.seed, round, wrong)
	}
	for name := range r.files {
		if _, ok := r.e.ByPath(strings.Split(name, "/")...); ok && strings.HasPrefix(name, "made/") {
			inDrive++
		}
	}
	r.d.requireCachedIsDrives(r.seed)
	r.d.start()
	for _, name := range r.names {
		if r.files[name] != nil {
			r.check(t, name)
		}
	}
	t.Logf("round %d: killed %v into the operation after %d whole ones, %d files read back", round, moment, whole,
		len(r.files))
	return inDrive
}
