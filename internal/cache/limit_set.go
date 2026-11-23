package cache

func (c *Cache) takeLimit(limit, dirty int64, by string) {
	if c.setLimit(limit, dirty, by) {
		c.tellLimit(limit, by)
	}
}

func (c *Cache) setLimit(limit, dirty int64, by string) bool {
	c.room.mu.Lock()
	defer c.room.mu.Unlock()
	c.room.limit, c.room.dirty, c.room.checked = limit, dirty, c.opt.Clock.Now()
	if diff := limit - c.room.said; max(diff, -diff) > c.room.said/limitSaidStep {
		c.log.Info("the cache limit changed", "bytes", limit, "was", c.room.said, "by", by)
		c.room.said = limit
	}
	switch low := limit < c.opt.MinLimit; {
	case low && !c.room.low:
		c.log.Warn("the cache limit fell below the least cache_size, as other data takes the disk", "bytes", limit, "least",
			c.opt.MinLimit)
		c.room.low = true
	case !low:
		c.room.low = false
	}
	diff := limit - c.room.told
	return by != c.room.toldBy || max(diff, -diff) > c.room.told/limitSaidStep
}

func (c *Cache) tellLimit(limit int64, by string) {
	if c.opt.LimitChanged != nil && c.opt.LimitChanged(limit, by) != nil {
		return
	}
	c.room.mu.Lock()
	c.room.told, c.room.toldBy = limit, by
	c.room.mu.Unlock()
}
