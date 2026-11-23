package meta

import "time"

type Standing int

const (
	Broken Standing = iota

	Synced

	ToCreate

	ToSendContent

	ToSendMeta

	GoneUnsent

	GoneInDoubt

	OnlyHere
)

func (s Standing) String() string {
	return [...]string{"broken", "synced", "to create", "to send content", "to send metadata",
		"gone unsent", "gone in doubt", "only here"}[s]
}

func (it Item) Standing() Standing {
	switch {
	case it.broken():
		return Broken
	case it.goneUnsent():

		return GoneUnsent
	case it.LocalOnly():
		return OnlyHere
	case !it.RExists && it.Trashed:
		return GoneInDoubt
	case !it.RExists:
		return ToCreate
	case it.Dirty:
		return ToSendContent
	case it.MetaDirty:
		return ToSendMeta
	}
	return Synced
}

func (it Item) goneUnsent() bool { return !it.RExists && it.Trashed && !it.CreateTried }

func (it Item) broken() bool {
	return it.Dirty && it.Kind != KindFile ||
		it.LocalOnly() && it.RExists ||
		it.Lost && it.Service
}

const (
	sqlGoesToDrive = `lost = 0 AND service = 0`

	sqlLost = `lost = 1`

	sqlNotLost = `lost = 0`

	sqlContentLocal = `(dirty = 1 OR r_exists = 0)`

	sqlEmptyUnsent = `r_exists = 0 AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent = items.id)`

	sqlGoneUnsent = `trashed = 1 AND r_exists = 0 AND create_tried = 0`
)

func (it Item) ContentLocal() bool { return !it.RExists || it.Dirty }

func (it Item) AheadOfDrive() bool { return it.ContentLocal() || it.MetaDirty }

func (it Item) LocalOnly() bool { return it.Lost || it.Service }

func (it Item) Waits() bool { return it.AheadOfDrive() && !it.LocalOnly() }

func (it Item) InDrive() bool { return it.RExists && !it.LocalOnly() }

func (it Item) NeverTried() bool { return !it.RExists && !it.CreateTried }

func (it Item) TrashedHere() bool { return it.Trashed && !it.RTrashed }

func (it Item) MetaWaits() bool { return it.RExists && it.MetaDirty }

func (it Item) RenamedHere() bool {
	return it.MetaDirty && (it.Name != it.RName || it.Parent != it.RParent)
}

func (it Item) metaAsDrive() bool {
	return it.RExists && it.MetaDirty && it.Name == it.RName && it.Parent == it.RParent &&
		it.Trashed == it.RTrashed && it.Mtime.Equal(it.RMtime)
}

func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}
