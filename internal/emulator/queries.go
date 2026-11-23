package emulator

func (e *Emulator) ByPath(path ...string) (File, bool) {
	parent := e.Root()
	var found File
	for _, name := range path {
		hits := 0
		for _, f := range e.Children(parent) {
			if f.Name == name {
				found = f
				hits++
			}
		}
		if hits != 1 {
			return File{}, false
		}
		parent = found.ID
	}
	return found, true
}

func (e *Emulator) All() []File {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []File
	for _, id := range e.order {
		f := e.files[id]
		if f == nil || id == e.root {
			continue
		}
		c := f.bare()
		c.Content = nil
		out = append(out, c)
	}
	return out
}

func (e *Emulator) Children(parent string) []File {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []File
	for _, id := range e.order {
		f := e.files[id]
		if f == nil || len(f.Parents) == 0 || f.Parents[0] != parent || e.trashed(f) {
			continue
		}
		out = append(out, f.clone())
	}
	return out
}

func (e *Emulator) Requests() []Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Request(nil), e.requests...)
}

func (e *Emulator) ResetRequests() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests = nil
	e.reads, e.writes = perSecond{}, perSecond{}
}
