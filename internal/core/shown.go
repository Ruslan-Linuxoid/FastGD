package core

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type Notifier interface {
	Entry(parent uint64, name string)

	Attrs(ino uint64)

	Content(ino uint64)
}

type noNotifier struct{}

func (noNotifier) Entry(uint64, string) {}
func (noNotifier) Attrs(uint64)         {}
func (noNotifier) Content(uint64)       {}

type shown struct {
	store shownRecords
	log   *slog.Logger

	notifier atomic.Pointer[Notifier]

	telling sync.WaitGroup
}

type shownRecords interface {
	Visible() meta.Visible
	ByID(id string) (meta.Item, error)
	ShownNameOf(it meta.Item) (string, error)
	SiblingNames(parent, base string) ([]string, error)
	Shortcuts() ([]meta.Item, error)
	ShortcutsTo(id string) ([]meta.Item, error)
}

func newShown(store shownRecords, log *slog.Logger) *shown {
	v := &shown{store: store, log: log}
	v.set(noNotifier{})
	return v
}

func (v *shown) made(change func(meta.Visible) error) error {
	visible := v.store.Visible()
	err := change(visible)
	if gone := visible.Pruned(); len(gone) > 0 {
		v.telling.Go(func() {
			for _, g := range gone {
				v.kernel().Entry(uint64(g.Parent), g.Name)
			}
		})
	}
	return err
}

func (v *shown) set(n Notifier) { v.notifier.Store(&n) }

func (v *shown) kernel() Notifier { return *v.notifier.Load() }

func (v *shown) local(change func(meta.Visible) error, items ...meta.Item) error {
	folder := false
	var ids []string
	var own []meta.Item
	for _, it := range items {
		if it.ID == "" {
			continue
		}
		ids = append(ids, it.ID)
		folder = folder || it.Kind == meta.KindDir
		if it.Kind == meta.KindShortcut {

			own = append(own, it)
		}
	}
	n, err := v.note(items, ids, folder)
	if err != nil {
		v.log.Warn("shortcuts of a changed item", "err", err)
	}
	n.links = append(own, n.links...)
	if err := v.made(change); err != nil {
		return err
	}
	after := v.names(n.groups)
	v.telling.Go(func() { v.tell(n, after) })
	return nil
}

func (v *shown) remote(
	items []meta.Item, ids []string, folder bool, apply func(meta.Visible) ([]meta.Applied, error),
) ([]meta.Applied, error) {
	n, err := v.note(items, ids, folder)
	if err != nil {
		return nil, err
	}
	var applied []meta.Applied
	err = v.made(func(visible meta.Visible) (err error) {
		applied, err = apply(visible)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, a := range applied {
		switch {
		case a.Before != nil && a.After == nil:
			v.kernel().Content(uint64(a.Before.Ino))
		case a.Before != nil:
			if contentChanged(*a.Before, *a.After) {
				v.kernel().Content(uint64(a.After.Ino))
			} else {
				v.kernel().Attrs(uint64(a.After.Ino))
			}
		}
	}
	v.tell(n, v.names(n.groups))
	return applied, nil
}

func (v *shown) attrs(change func(meta.Visible) ([]meta.Ino, error)) error {
	var inos []meta.Ino
	err := v.made(func(visible meta.Visible) (err error) {
		inos, err = change(visible)
		return err
	})
	if err != nil {
		return err
	}
	v.restated(inos...)
	return nil
}

func (v *shown) restated(inos ...meta.Ino) {
	if len(inos) == 0 {
		return
	}
	v.telling.Go(func() {
		for _, ino := range inos {
			v.kernel().Attrs(uint64(ino))
		}
	})
}

func (v *shown) stale(items ...meta.Item) {
	type entry struct {
		parent meta.Ino
		name   string
	}
	var entries []entry
	for _, it := range items {
		parent, err := v.store.ByID(it.Parent)
		if err != nil {
			continue
		}
		if name, err := v.store.ShownNameOf(it); err == nil && name != "" {
			entries = append(entries, entry{parent.Ino, name})
		}
	}
	v.telling.Go(func() {
		for _, e := range entries {
			v.kernel().Entry(uint64(e.parent), e.name)
		}
	})
}

func (v *shown) named(parent meta.Ino, name string) {
	v.telling.Go(func() { v.kernel().Entry(uint64(parent), name) })
}

func (v *shown) rewritten(ino meta.Ino) {
	v.telling.Go(func() { v.kernel().Content(uint64(ino)) })
}
