package meta

import "testing"

func flagged(bits int) Item {
	on := func(n int) bool { return bits&(1<<n) != 0 }
	it := Item{Kind: KindFile, RExists: on(0), Dirty: on(1), MetaDirty: on(2), CreateTried: on(3),
		Trashed: on(4), RTrashed: on(5), Lost: on(6), Service: on(7)}
	if on(8) {
		it.Kind = KindDir
	}
	return it
}

var standingChecks = map[Standing]func(it Item) bool{
	Broken: func(Item) bool { return true },
	Synced: func(it Item) bool { return !it.AheadOfDrive() && it.InDrive() },
	ToCreate: func(it Item) bool {
		return it.Waits() && !it.RExists && !it.Trashed
	},
	ToSendContent: func(it Item) bool { return it.Waits() && it.InDrive() && it.ContentLocal() },
	ToSendMeta:    func(it Item) bool { return it.Waits() && it.MetaWaits() && !it.ContentLocal() },
	GoneUnsent:    func(it Item) bool { return it.NeverTried() && it.Trashed },
	GoneInDoubt: func(it Item) bool {
		return it.Waits() && !it.RExists && it.Trashed && !it.NeverTried()
	},
	OnlyHere: func(it Item) bool { return it.LocalOnly() && !it.Waits() && !it.InDrive() },
}

func requireStandingAgrees(t *testing.T, it Item, st Standing) {
	t.Helper()
	check, known := standingChecks[st]
	if !known {
		t.Fatalf("%+v has the unknown standing %d", it, st)
	}
	if !check(it) {
		t.Fatalf("%+v stands %v against what it answers", it, st)
	}
}

func contradictsItself(it Item) bool {
	return it.Dirty && it.Kind != KindFile || (it.Lost || it.Service) && it.RExists || it.Lost && it.Service
}

func TestStandingOfEverySetOfFlags(t *testing.T) {
	seen := map[Standing]int{}
	for bits := range 1 << 9 {
		it := flagged(bits)
		st := it.Standing()
		seen[st]++
		contradicts := contradictsItself(it)
		if (st == Broken) != contradicts {
			t.Fatalf("%+v stands %v, contradiction %v", it, st, contradicts)
		}
		requireStandingAgrees(t, it, st)
	}
	for st := Broken; st <= OnlyHere; st++ {
		if seen[st] == 0 {
			t.Fatalf("no set of flags stands %v", st)
		}
	}
}

func TestStandingOfItemsAsTheyCome(t *testing.T) {
	file, dir := KindFile, KindDir
	for name, c := range map[string]struct {
		it   Item
		want Standing
	}{
		"a file loaded from Drive":            {Item{Kind: file, RExists: true}, Synced},
		"a file written here":                 {Item{Kind: file, Dirty: true}, ToCreate},
		"a folder made here":                  {Item{Kind: dir}, ToCreate},
		"a file made here and renamed":        {Item{Kind: file, Dirty: true, MetaDirty: true}, ToCreate},
		"a file whose create is on its way":   {Item{Kind: file, Dirty: true, CreateTried: true}, ToCreate},
		"a file of Drive edited here":         {Item{Kind: file, RExists: true, Dirty: true}, ToSendContent},
		"an edited file renamed too":          {Item{Kind: file, RExists: true, Dirty: true, MetaDirty: true}, ToSendContent},
		"a file created whose mark stays":     {Item{Kind: file, RExists: true, CreateTried: true}, Synced},
		"a file of Drive renamed here":        {Item{Kind: file, RExists: true, MetaDirty: true}, ToSendMeta},
		"a file of Drive trashed here":        {Item{Kind: file, RExists: true, MetaDirty: true, Trashed: true}, ToSendMeta},
		"a file trashed in Drive":             {Item{Kind: file, RExists: true, Trashed: true, RTrashed: true}, Synced},
		"a new file removed at once":          {Item{Kind: file, Dirty: true, Trashed: true}, GoneUnsent},
		"a new file removed during its send":  {Item{Kind: file, Dirty: true, Trashed: true, CreateTried: true}, GoneInDoubt},
		"a local version in Lost and Found":   {Item{Kind: file, Dirty: true, Lost: true}, OnlyHere},
		"a folder of Lost and Found":          {Item{Kind: dir, Lost: true}, OnlyHere},
		"a renamed file of Lost and Found":    {Item{Kind: file, Dirty: true, Lost: true, MetaDirty: true}, OnlyHere},
		"a lock file of an editor":            {Item{Kind: file, Dirty: true, Service: true}, OnlyHere},
		"a lock file removed":                 {Item{Kind: file, Dirty: true, Service: true, Trashed: true}, GoneUnsent},
		"a removed file of Lost and Found":    {Item{Kind: file, Dirty: true, Lost: true, Trashed: true}, GoneUnsent},
		"a dirty folder":                      {Item{Kind: dir, RExists: true, Dirty: true}, Broken},
		"a file of Drive in Lost and Found":   {Item{Kind: file, RExists: true, Lost: true}, Broken},
		"a service item Drive holds":          {Item{Kind: file, RExists: true, Service: true}, Broken},
		"trashed in Drive before its answer":  {Item{Kind: file, Dirty: true, CreateTried: true, RTrashed: true}, ToCreate},
		"that one removed here too, untried":  {Item{Kind: file, Dirty: true, Trashed: true, RTrashed: true}, GoneUnsent},
		"of Lost and Found and a service one": {Item{Kind: file, Dirty: true, Lost: true, Service: true}, Broken},
	} {
		if got := c.it.Standing(); got != c.want {
			t.Errorf("%s stands %v, want %v", name, got, c.want)
		}
	}
}
