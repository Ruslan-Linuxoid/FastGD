package emulator

import (
	"os"
	"syscall"
)

func KeepContentIn(dir string) Option { return func(e *Emulator) { e.contentDir = dir } }

func (e *Emulator) room(n int64) []byte {
	if e.contentDir == "" || n == 0 {
		return make([]byte, 0, n)
	}
	f, err := os.CreateTemp(e.contentDir, "content-*")
	if err != nil {
		panic(err)
	}

	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Truncate(n); err != nil {
		panic(err)
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(n), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		panic(err)
	}
	e.mappedMu.Lock()
	defer e.mappedMu.Unlock()
	if e.mapped == nil {
		e.mapped = map[*byte]bool{}
	}
	e.mapped[&data[0]] = true
	return data[:0]
}

func (e *Emulator) free(b []byte) {
	if cap(b) == 0 {
		return
	}
	whole := b[:cap(b)]
	e.mappedMu.Lock()
	defer e.mappedMu.Unlock()
	if !e.mapped[&whole[0]] {
		return
	}
	delete(e.mapped, &whole[0])
	if err := syscall.Munmap(whole); err != nil {
		panic(err)
	}
}
