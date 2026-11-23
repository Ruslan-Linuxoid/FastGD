package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const LostFolder = "lost-and-found"

var (
	ErrNotFolder = errors.New("not a folder")

	ErrNoFileID = errors.New("the folder is not in Drive yet: try again once it is uploaded")
)

const (
	GoneTrash   = "in Drive's trash"
	GoneDeleted = "deleted in Drive, or gone from My Drive"
)

type Folder struct {
	Path string

	Ino uint64

	Gone string
}

func (f *FS) FolderAt(path string) (string, error) {
	path = strings.Trim(path, "/")
	if path == meta.LostName {
		return LostFolder, nil
	}
	id, _, err := f.store.GetState(meta.StateRootID)
	if err != nil {
		return "", err
	}
	if path != "" {
		for name := range strings.SplitSeq(path, "/") {
			it, err := f.store.Lookup(id, name)
			if err != nil {
				return "", fmt.Errorf("%s: %w", path, errno(err))
			}
			if it.Kind != meta.KindDir {
				return "", fmt.Errorf("%s: %w", path, ErrNotFolder)
			}
			id = it.ID
		}
	}
	if meta.IsLocalKey(id) {
		return "", fmt.Errorf("%s: %w", path, ErrNoFileID)
	}
	return id, nil
}

func (f *FS) Folder(id string) (Folder, error) {
	if id == LostFolder {
		return f.lostFolder()
	}
	it, err := f.store.ByID(id)
	if errors.Is(err, meta.ErrNotFound) {
		return Folder{Gone: GoneDeleted}, nil
	}
	if err != nil {
		return Folder{}, err
	}
	path, err := f.store.Path(id)
	if errors.Is(err, meta.ErrNotShown) {
		return f.whyNotShown(it)
	}
	if err != nil {
		return Folder{}, err
	}
	return Folder{Path: path, Ino: uint64(it.Ino)}, nil
}

func (f *FS) lostFolder() (Folder, error) {
	root, _, err := f.store.GetState(meta.StateRootID)
	if err != nil {
		return Folder{}, err
	}
	it, err := f.store.Lookup(root, meta.LostName)
	if errors.Is(err, meta.ErrNotFound) {
		return Folder{Path: meta.LostName}, nil
	}
	if err != nil {
		return Folder{}, err
	}
	return Folder{Path: meta.LostName, Ino: uint64(it.Ino)}, nil
}

func (f *FS) whyNotShown(it meta.Item) (Folder, error) {
	for range maxFolderDepth {
		if it.Trashed {
			return Folder{Gone: GoneTrash}, nil
		}
		if it.Parent == "" {
			break
		}
		above, err := f.store.ByID(it.Parent)
		if errors.Is(err, meta.ErrNotFound) {
			break
		}
		if err != nil {
			return Folder{}, err
		}
		it = above
	}
	return Folder{Gone: GoneDeleted}, nil
}

const maxFolderDepth = 10000
