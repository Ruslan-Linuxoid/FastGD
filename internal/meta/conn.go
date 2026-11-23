package meta

import (
	"context"
	"database/sql"
)

type conn struct{ *sql.DB }

type txn struct{ *sql.Tx }

type prepared struct{ *sql.Stmt }

func (c conn) Exec(query string, args ...any) (sql.Result, error) {
	return c.ExecContext(context.Background(), query, args...)
}

func (c conn) Query(query string, args ...any) (*sql.Rows, error) {
	return c.QueryContext(context.Background(), query, args...)
}

func (c conn) QueryRow(query string, args ...any) *sql.Row {
	return c.QueryRowContext(context.Background(), query, args...)
}

func (c conn) Begin() (txn, error) {
	tx, err := c.BeginTx(context.Background(), nil)
	return txn{tx}, err
}

func (t txn) Exec(query string, args ...any) (sql.Result, error) {
	return t.ExecContext(context.Background(), query, args...)
}

func (t txn) Query(query string, args ...any) (*sql.Rows, error) {
	return t.QueryContext(context.Background(), query, args...)
}

func (t txn) QueryRow(query string, args ...any) *sql.Row {
	return t.QueryRowContext(context.Background(), query, args...)
}

func (t txn) Prepare(query string) (prepared, error) {
	st, err := t.PrepareContext(context.Background(), query)
	return prepared{st}, err
}

func (p prepared) Exec(args ...any) (sql.Result, error) {
	return p.ExecContext(context.Background(), args...)
}
