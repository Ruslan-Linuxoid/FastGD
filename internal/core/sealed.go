package core

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"sync/atomic"

	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
)

const sealing = -1

type sealed struct {
	bytes []byte
	md5   string
}

func (f *FS) seal(st *edited.State, snap snapshot) (sealed, error) {
	from := tailFrom(snap.size)
	last := make([]byte, snap.size-from)

	h := md5.New()
	_, err := io.Copy(h, io.NewSectionReader(snap.fd, 0, from))
	if err == nil {
		_, err = snap.fd.ReadAt(last, from)
	}
	if f.beforeChunk != nil {
		f.beforeChunk(sealing)
	}

	if st.Outdated(snap.gen) {
		return sealed{}, errOutdated
	}
	if err != nil {
		return sealed{}, fmt.Errorf("read the local content: %w", err)
	}
	h.Write(last)
	return sealed{bytes: last, md5: hex.EncodeToString(h.Sum(nil))}, nil
}

type sealedBody struct {
	st   *edited.State
	snap snapshot
	off  int64

	tail []byte
	held bool

	sum hash.Hash

	outdated *atomic.Bool
}

func (b *sealedBody) Read(p []byte) (int, error) {
	if b.outdated.Load() {
		return 0, errOutdated
	}
	from := tailFrom(b.snap.size)
	if b.off < from {
		n, err := b.snap.fd.ReadAt(p[:min(int64(len(p)), from-b.off)], b.off)
		if n == 0 {
			return 0, b.cutShort(err)
		}
		b.sum.Write(p[:n])
		b.off += int64(n)
		return n, nil
	}
	if !b.held {
		b.tail = make([]byte, b.snap.size-from)
		_, err := b.snap.fd.ReadAt(b.tail, from)
		if err != nil || b.st.Outdated(b.snap.gen) {
			return 0, b.cutShort(err)
		}
		b.sum.Write(b.tail)
		b.held = true
	}
	if b.off == b.snap.size {
		return 0, io.EOF
	}
	n := copy(p, b.tail[b.off-from:])
	b.off += int64(n)
	return n, nil
}

func (b *sealedBody) cutShort(err error) error {
	if b.st.Outdated(b.snap.gen) {
		b.outdated.Store(true)
		return errOutdated
	}
	return fmt.Errorf("read the local content: %v", err)
}
