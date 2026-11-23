package meta

import (
	"errors"
	"fmt"
)

type Piece struct {
	Off, Len int64
}

func (s *Store) Pieces(ino Ino) ([]Piece, error) {
	rows, err := s.r.Query(`SELECT off, len FROM pieces WHERE ino = ? ORDER BY off`, ino)
	if err != nil {
		return nil, fmt.Errorf("meta: pieces: %w", err)
	}
	defer rows.Close()
	var out []Piece
	for rows.Next() {
		var p Piece
		if err := rows.Scan(&p.Off, &p.Len); err != nil {
			return nil, fmt.Errorf("meta: pieces: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddPiece(ino Ino, off, n int64) error {
	return exec(s.w, "add piece", `INSERT INTO pieces(ino, off, len) VALUES (?, ?, ?)`, ino, off, n)
}

func (s *Store) ReplacePieces(ino Ino, pieces []Piece) error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("meta: replace pieces: %w", err)
	}
	defer tx.Rollback()
	old, err := recordedPieces(tx, ino)
	if err != nil {
		return err
	}
	stay := make(map[Piece]bool, len(pieces))
	for _, p := range pieces {
		if stay[p] {
			return fmt.Errorf("meta: replace pieces: the piece at %d comes twice", p.Off)
		}
		stay[p] = true
	}

	if err := dropPieces(tx, ino, old, stay); err != nil {
		return err
	}
	if err := addPieces(tx, ino, pieces, stay); err != nil {
		return err
	}
	return commit(tx)
}

func recordedPieces(tx txn, ino Ino) ([]Piece, error) {
	rows, err := tx.Query(`SELECT off, len FROM pieces WHERE ino = ?`, ino)
	if err != nil {
		return nil, fmt.Errorf("meta: replace pieces: %w", err)
	}
	defer rows.Close()
	var old []Piece
	for rows.Next() {
		var p Piece
		if err := rows.Scan(&p.Off, &p.Len); err != nil {
			rows.Close()
			return nil, fmt.Errorf("meta: replace pieces: %w", err)
		}
		old = append(old, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("meta: replace pieces: %w", err)
	}
	return old, nil
}

func dropPieces(tx txn, ino Ino, old []Piece, stay map[Piece]bool) error {
	for _, p := range old {
		if stay[p] {
			delete(stay, p)
			continue
		}
		if _, err := tx.Exec(`DELETE FROM pieces WHERE ino = ? AND off = ?`, ino, p.Off); err != nil {
			return fmt.Errorf("meta: replace pieces: %w", err)
		}
	}
	return nil
}

func addPieces(tx txn, ino Ino, pieces []Piece, stay map[Piece]bool) error {
	for _, p := range pieces {
		if !stay[p] {
			continue
		}
		delete(stay, p)
		if _, err := tx.Exec(`INSERT INTO pieces(ino, off, len) VALUES (?, ?, ?)`, ino, p.Off, p.Len); err != nil {
			return fmt.Errorf("meta: replace pieces: %w", err)
		}
	}
	return nil
}

func (s *Store) DeletePieces(ino Ino) error {
	return exec(s.w, "delete pieces", `DELETE FROM pieces WHERE ino = ?`, ino)
}
