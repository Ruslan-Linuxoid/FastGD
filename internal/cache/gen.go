package cache

import (
	"cmp"
	"os"
	"slices"
	"sync"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type gen struct {
	num uint64

	recMu  sync.Mutex
	pieces spans

	joinAt int

	synced bool

	inflight []*fetch
	stale    bool

	fd   *os.File
	refs int

	local bool

	extentPageReserved bool

	check *check
}

func (c *Cache) newGen(pieces spans) *gen {
	return &gen{
		num: c.genNums.Add(1), pieces: pieces, joinAt: joinAbove, synced: len(pieces) > 0,
		extentPageReserved: len(pieces) > 0,
	}
}

func (c *Cache) Generation(ino meta.Ino) (uint64, error) {
	g, err := c.current(ino)
	if err != nil {
		return 0, err
	}
	return g.num, nil
}

func (g *gen) refuses(f File) error {
	switch {
	case g.stale || f.Gen != 0 && f.Gen != g.num:
		return errSwitched
	case g.local:
		return ErrLocal
	}
	return nil
}

func (g *gen) missing(want span) []span {
	coming := make(spans, 0, len(g.inflight))
	for _, fl := range g.inflight {
		coming = append(coming, fl.span)
	}
	slices.SortFunc(coming, func(a, b span) int { return cmp.Compare(a.off, b.off) })
	var out []span
	for _, gap := range g.pieces.gaps(want) {
		out = append(out, coming.gaps(gap)...)
	}
	return out
}

func (g *gen) drop(fl *fetch) {
	g.inflight = slices.DeleteFunc(g.inflight, func(x *fetch) bool { return x == fl })
}

func (c *Cache) noteFetching(g *gen) {
	if len(g.inflight) > 0 {
		c.fetching[g] = struct{}{}
	} else {
		delete(c.fetching, g)
	}
}

func (g *gen) roomOf() int64 {
	n := g.pieces.bytesOn()
	if g.extentPageReserved {
		n += page()
	}
	return n
}

func (c *Cache) current(ino meta.Ino) (*gen, error) {
	c.mu.Lock()
	g := c.gens[ino]
	c.mu.Unlock()
	if g != nil {
		return g, nil
	}
	recs, err := c.opt.Records.Pieces(ino)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.gens[ino]; g != nil {
		return g, nil
	}
	pieces := make(spans, 0, len(recs))
	for _, r := range recs {
		pieces = append(pieces, pieceSpan(r.Off, r.Len))
	}
	g = c.newGen(pieces)
	c.gens[ino] = g
	return g, nil
}

func (c *Cache) Downloading() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, g := range c.gens {
		n += len(g.inflight)
	}
	return n
}
