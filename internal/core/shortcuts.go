package core

import (
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) Readlink(ino uint64) (string, error) {
	it, err := f.item(ino)
	if err != nil {
		return "", err
	}
	if it.Kind != meta.KindShortcut {
		return "", syscall.EINVAL
	}
	link, err := f.store.Link(it)
	if err != nil {
		return "", errno(err)
	}
	if len(link) >= maxLink {

		return "", syscall.ENAMETOOLONG
	}
	return link, nil
}

const maxLink = 4096
