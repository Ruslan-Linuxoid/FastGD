package meta

import (
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const slashStandIn = "／"

const nulStandIn = "␀"

var shownBytes = strings.NewReplacer("/", slashStandIn, "\x00", nulStandIn)

func (it Item) fullName() string {
	return shownBytes.Replace(it.Name) + linkTypeOf(it.Mime).ext
}

func (it Item) GroupName() string {
	name := it.fullName()
	if len(name) <= MaxName {
		return name
	}
	stem, ext := splitExt(name)
	return fitted(stem, "", ext)
}

func fitted(stem, tail, ext string) string {
	return cutUTF8(stem, MaxName-len(tail)-len(ext)) + tail + ext
}

func DriveNameOf(it Item, display string) string {
	if ext := linkTypeOf(it.Mime).ext; ext != "" && hasExt(display, ext) {
		display = display[:len(display)-len(ext)]
	}
	return DriveName(display)
}

const MaxName = 1024

func plainAllowed(dname string) bool { return dname != "" && dname != "." && dname != ".." }

func numbered(dname string, n int) string {
	stem, ext := splitExt(dname)
	num := "(" + strconv.Itoa(n) + ")"
	if stem != "" {
		num = " " + num
	}
	return fitted(stem, num, ext)
}

func cutUTF8(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

const numberRoom = 32

func splitExt(name string) (stem, ext string) {
	ext = path.Ext(name)
	stem = name[:len(name)-len(ext)]
	if stem == "" || ext == "." || len(ext)+numberRoom > MaxName {

		return name, ""
	}
	return stem, ext
}

func DriveName(display string) string { return strings.ReplaceAll(display, slashStandIn, "/") }
