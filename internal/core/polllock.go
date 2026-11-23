package core

import "context"

type pollLock chan struct{}

func (l pollLock) Lock() { l <- struct{}{} }

func (l pollLock) Unlock() { <-l }

func (l pollLock) LockCtx(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
