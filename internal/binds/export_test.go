package binds

func SetOurs(m *Manager, ours func(dir string) bool) { m.ours = ours }
