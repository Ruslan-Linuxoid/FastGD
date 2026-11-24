//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

const (
	paceFolders = 50
	paceFiles   = 50
)

func walkOne(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeFilesUntil(t *testing.T, dir string, stop <-chan struct{}) <-chan int {
	t.Helper()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrote := make(chan int, 1)
	go func() {
		n := 0
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				wrote <- n
				return
			case <-tick.C:
			}
			if err := os.WriteFile(fmt.Sprintf("%s/%d.txt", dir, n), []byte("copied"), 0o644); err != nil {
				t.Errorf("a write of a copied file: %v", err)
			}
			n++
		}
	}()
	return wrote
}

func TestSecondNamesAnswerAtOnceWhileFilesAreWritten(t *testing.T) {
	e := startEmu(t)
	names := make([]string, 0, paceFolders*paceFiles)
	for i := range paceFolders {
		folder := e.Put(emulator.File{Name: fmt.Sprintf("D%02d", i), MimeType: emulator.MimeFolder, SharedWithMe: true})
		for j := range paceFiles {
			name := fmt.Sprintf("f%02d-%02d.txt", i, j)
			e.Put(emulator.File{Name: name, Parents: []string{folder}, Content: []byte(name), SharedWithMe: true})
			names = append(names, name)
		}
	}
	d := newDaemon(t, e)
	d.start()
	if got := len(dirNames(t, d.path("/Shared with me"))); got != paceFolders*(paceFiles+1) {
		t.Fatalf("Shared with me lists %d names, want %d", got, paceFolders*(paceFiles+1))
	}
	stop := make(chan struct{})
	wrote := writeFilesUntil(t, d.path("copy"), stop)
	var took []time.Duration
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		at := time.Now()
		if _, err := os.Stat(d.path("/Shared with me/" + names[len(took)%len(names)])); err != nil {
			t.Fatal(err)
		}
		took = append(took, time.Since(at))
	}
	walked := time.Now()
	for _, name := range names {
		walkOne(t, d.path("/Shared with me/"+name))
	}
	walk := time.Since(walked)
	close(stop)
	n := <-wrote
	slices.Sort(took)
	p99 := took[len(took)*99/100]
	t.Logf("%d stats, p50 %v, p99 %v, max %v; the walk of %d names %v; %d files written", len(took),
		took[len(took)/2], p99, took[len(took)-1], len(names), walk, n)
	if n < 10 {
		t.Fatalf("only %d files were written meanwhile", n)
	}
	if p99 > 5*time.Millisecond {
		t.Errorf("p99 of a stat of a second name while files are written is %v, want 5ms at most", p99)
	}
	if walk > 10*time.Second {
		t.Errorf("the walk of the second names while files are written took %v, want under 10s", walk)
	}
}
