package core

import (
	"context"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type step struct {
	name string
	run  func() error
}

func (f *FS) startup(ctx context.Context) []step {
	always := func(fn func()) func() error {
		return func() error {
			fn()
			return nil
		}
	}
	return []step{

		{"load the tree from Drive", func() error {
			_, loaded, err := f.store.GetState(meta.StateLoaded)
			if err != nil || loaded {
				return err
			}
			return f.initialLoad(ctx)
		}},

		{"drop the clean content counted by another unit of holes", f.recountByUnit},

		{"drop the content of an older layout", f.dropOldLayout},
		{"drop the exports of an older build", f.dropExports},

		{"finish the moves into Lost and Found", f.conflicts.resume},
		{"finish the swaps of content", f.unswap},

		{"drop the fileIds the cache holds content under", func() error { return f.store.DropIDs(f.cache.Holds) }},
		{"count the content no item has", f.cache.CountStrays},

		{"read the rights the database lacks", always(func() { f.loadRights(ctx) })},

		{"restore what the state set aside had not sent", f.restore},

		{"requeue the operations that were being sent", func() error { return f.store.RequeueSending() }},
		{"give the waiting items their operations", f.store.SeedOps},

		{"take up the files changed here", f.edits.Recover},

		{"forget the items trashed before they went to Drive", f.forgetUnsent},

		{"read the tree anew if the fields asked of Drive are others", func() error { return f.rereadTree(ctx) }},

		{"read the quota kept", always(f.loadQuota)},
		{"read the refusal of uploads kept", always(f.loadRefusal)},
		{"read the refusal of downloads kept", always(f.loadDownloadRefusal)},
	}
}
