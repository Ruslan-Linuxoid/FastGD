package cache

import "github.com/Ruslan-Linuxoid/FastGD/internal/meta"

type Records interface {
	Holdings

	DirtyIDs() ([]string, error)

	ByID(id string) (meta.Item, error)

	Knows(id string) (bool, error)

	Pieces(ino meta.Ino) ([]meta.Piece, error)

	AddPiece(ino meta.Ino, off, n int64) error

	ReplacePieces(ino meta.Ino, pieces []meta.Piece) error

	DeletePiece(ino meta.Ino, off int64) error

	DeletePieces(ino meta.Ino) error
}

type Holdings interface {
	CachedPieces() ([]meta.CachedPiece, error)

	Dirty() ([]meta.Item, error)
}

func cleanRoom(pieces []meta.CachedPiece) int64 {
	var n int64
	files := map[meta.Ino]struct{}{}
	for _, p := range pieces {
		n += PageUp(p.Len)
		files[p.Ino] = struct{}{}
	}
	return n + int64(len(files))*page()
}

func Held(h Holdings) (int64, error) {
	pieces, err := h.CachedPieces()
	if err != nil {
		return 0, err
	}
	local, err := h.Dirty()
	if err != nil {
		return 0, err
	}
	var n int64
	for _, p := range pieces {
		n += p.Len
	}
	for _, it := range local {
		if it.LocalOnly() && it.Trashed {
			continue
		}
		n += it.Size
	}
	return n, nil
}
