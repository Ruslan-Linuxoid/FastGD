package core

import (
	"slices"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type group struct {
	parent, name string
}

type groupSet map[group]struct{}

func (gs groupSet) add(items ...meta.Item) {
	for _, it := range items {
		gs[group{it.Parent, it.GroupName()}] = struct{}{}
	}
}

type noted struct {
	groups groupSet
	before map[group][]string
	links  []meta.Item
}

func (v *shown) note(items []meta.Item, ids []string, folder bool) (noted, error) {
	n := noted{groups: groupSet{}}
	n.groups.add(items...)
	var err error
	if folder {
		n.links, err = v.store.Shortcuts()
	} else {
		for _, id := range ids {
			var sc []meta.Item
			if sc, err = v.store.ShortcutsTo(id); err != nil {
				break
			}
			n.links = append(n.links, sc...)
		}
	}
	n.groups.add(n.links...)
	n.before = v.names(n.groups)
	return n, err
}

func (v *shown) names(gs groupSet) map[group][]string {
	out := make(map[group][]string, len(gs))
	for g := range gs {
		names, err := v.store.SiblingNames(g.parent, g.name)
		if err != nil {
			v.log.Warn("names of a changed folder", "parent", g.parent, "err", err)
			continue
		}
		out[g] = names
	}
	return out
}

func (v *shown) tell(n noted, after map[group][]string) {
	for _, l := range n.links {
		v.kernel().Attrs(uint64(l.Ino))
	}
	for g := range n.groups {
		parent, err := v.store.ByID(g.parent)
		if err != nil {
			continue
		}

		names := slices.Concat(n.before[g], after[g])
		slices.Sort(names)
		for _, name := range slices.Compact(names) {
			v.kernel().Entry(uint64(parent.Ino), name)
		}
	}
}
