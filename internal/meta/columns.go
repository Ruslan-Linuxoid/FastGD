package meta

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

type itemColumn struct {
	name string

	drive bool

	at func(it *Item) any
}

var itemColumns = []itemColumn{
	{"ino", false, func(it *Item) any { return &it.Ino }},
	{"id", false, func(it *Item) any { return &it.ID }},
	{"kind", false, func(it *Item) any { return &it.Kind }},
	{"mime", true, func(it *Item) any { return &it.Mime }},
	{"name", false, func(it *Item) any { return &it.Name }},
	{"parent", false, func(it *Item) any { return &it.Parent }},
	{"dname", false, nil},
	{"trashed", false, func(it *Item) any { return &it.Trashed }},
	{"size", false, func(it *Item) any { return &it.Size }},
	{"md5", true, func(it *Item) any { return &it.MD5 }},
	{"version", true, func(it *Item) any { return &it.Version }},
	{"mtime", false, func(it *Item) any { return (*dbTime)(&it.Mtime) }},
	{"ctime", false, func(it *Item) any { return (*dbTime)(&it.Ctime) }},
	{"r_exists", true, func(it *Item) any { return &it.RExists }},
	{"dirty", false, func(it *Item) any { return &it.Dirty }},
	{"r_md5", true, func(it *Item) any { return &it.RMD5 }},
	{"r_version", true, func(it *Item) any { return &it.RVersion }},
	{"content_gen", false, func(it *Item) any { return &it.Gen }},
	{"upload_after", false, func(it *Item) any { return (*dbTime)(&it.UploadAfter) }},
	{"meta_dirty", false, func(it *Item) any { return &it.MetaDirty }},
	{"r_name", true, func(it *Item) any { return &it.RName }},
	{"r_parent", true, func(it *Item) any { return &it.RParent }},
	{"r_trashed", true, func(it *Item) any { return &it.RTrashed }},
	{"r_mtime", true, func(it *Item) any { return (*dbTime)(&it.RMtime) }},
	{"target", false, func(it *Item) any { return &it.ShortcutTarget }},
	{"denied", true, func(it *Item) any { return &it.Denied }},
	{"base", false, func(it *Item) any { return &it.Base }},
	{"base_tail", false, func(it *Item) any { return &it.BaseTail }},
	{"create_tried", true, func(it *Item) any { return &it.CreateTried }},
	{"rev", true, func(it *Item) any { return &it.Rev }},
	{"lost", false, func(it *Item) any { return &it.Lost }},
	{"service", false, func(it *Item) any { return &it.Service }},
	{"note_for", false, nil},
	{"shown_as", false, func(it *Item) any { return &it.shownAs }},
}

func columnNames(drive bool) string {
	var names []string
	for _, c := range itemColumns {
		if c.drive || !drive {
			names = append(names, c.name)
		}
	}
	return strings.Join(names, ", ")
}

func driveValues(it Item) []any {
	var out []any
	for _, c := range itemColumns {
		if c.drive {
			out = append(out, c.at(&it))
		}
	}
	return out
}

type dbTime time.Time

func (t *dbTime) Scan(v any) error {
	n, ok := v.(int64)
	if !ok {
		return fmt.Errorf("meta: a time of the database is %T, not a number", v)
	}
	*t = dbTime(fromNanos(n))
	return nil
}

func (t dbTime) Value() (driver.Value, error) { return nanos(time.Time(t)), nil }
