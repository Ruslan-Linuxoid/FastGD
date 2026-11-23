package meta

type Visible struct {
	*Store

	gone *[]Gone
}

type Gone struct {
	Parent Ino
	Name   string
}

func (s *Store) Visible() Visible { return Visible{s, new([]Gone)} }

func (s Visible) Pruned() []Gone { return *s.gone }
