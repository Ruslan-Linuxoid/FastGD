package drive

import (
	"context"
	"math/rand/v2"
	"time"
)

const (
	retryFirst = 200 * time.Millisecond
	retryMax   = 5 * time.Second

	bgRetryMax = 2 * time.Minute
)

type backgroundKey struct{}

func InBackground(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

func Promptly(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, false)
}

type deferringKey struct{}

func Deferring(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferringKey{}, true)
}

type onRetryKey struct{}

func OnRetry(ctx context.Context, fn func(err error, wait time.Duration)) context.Context {
	return context.WithValue(ctx, onRetryKey{}, fn)
}

func (c *Client) do(ctx context.Context, price cost, call func() error) error {
	r, within, cancel := c.newRetryRun(ctx)
	defer cancel()
	for {
		s, err := c.turn(within, price)
		if err != nil {
			return err
		}
		v, after, err := judge(ctx, call(), price.upload)
		c.spent(s, v, price)
		if ended, err2 := r.settles(v, after, err); ended {
			return err2
		}

		next := max(after, r.wait/2+rand.N(r.wait/2+1))
		if r.givesUp(v, after, next) {
			return retryLater(after, err)
		}
		if tell, ok := ctx.Value(onRetryKey{}).(func(error, time.Duration)); ok {
			tell(err, next)
		}
		if err := sleep(ctx, next); err != nil {
			return err
		}
		r.longest = r.wait == r.top
		r.wait = min(r.wait*2, r.top)
	}
}

type retryRun struct {
	c                     *Client
	start                 time.Time
	background, deferring bool
	wasOffline            bool
	top, wait             time.Duration

	longest bool
}

func (c *Client) newRetryRun(ctx context.Context) (*retryRun, context.Context, context.CancelFunc) {
	start := time.Now()
	within := ctx
	r := &retryRun{c: c, start: start, top: bgRetryMax, wait: retryFirst}
	r.background, _ = ctx.Value(backgroundKey{}).(bool)
	r.deferring, _ = ctx.Value(deferringKey{}).(bool)
	cancel := func() {}
	if !r.background {
		within, cancel = context.WithDeadline(ctx, start.Add(c.pace.lim.RetryFor))
		r.top = retryMax
	}
	r.wasOffline = c.pace.isOffline()
	return r, within, cancel
}

func (r *retryRun) settles(v verdict, after time.Duration, err error) (bool, error) {
	switch {
	case v == passed:
		return true, nil
	case v == refused || v == unknown:
		return true, err
	case v == offline && r.wasOffline:
		return true, err
	case r.deferring && v.busy():
		return true, retryLater(after, err)
	}
	return false, nil
}

func (r *retryRun) givesUp(v verdict, after, next time.Duration) bool {
	if r.background && v != offline {
		return r.longest || after > r.top
	}
	return time.Since(r.start)+next > r.c.pace.lim.RetryFor
}

func retryLater(after time.Duration, err error) error {
	if after > 0 {
		return &RetryLaterError{After: after, Err: err}
	}
	return err
}

func (c *Client) once(ctx context.Context, price cost, call func() error) error {
	s, err := c.turn(ctx, price)
	if err != nil {
		return err
	}
	v, _, err := judge(ctx, call(), price.upload)
	c.spent(s, v, price)
	return err
}

func (c *Client) turn(ctx context.Context, price cost) (*spend, error) {
	rate := c.pace.rate(price.write)
	for {
		if err := rate.take(ctx); err != nil {
			return nil, err
		}
		s, wait := c.pace.tryTake(price.units)
		if s != nil {
			return s, nil
		}
		rate.giveBack()
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (c *Client) spent(s *spend, v verdict, price cost) {
	if v == offline {
		c.pace.giveBack(s)
	} else {
		c.units.Add(int64(price.units))
	}
	if v == rateLimited {
		c.pace.rate(price.write).refused()
	}
	c.pace.note(v)
}

func get[T any](ctx context.Context, c *Client, price cost, call func() (T, error)) (T, error) {
	var out T
	err := c.do(ctx, price, func() (err error) {
		out, err = call()
		return err
	})
	return out, err
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
