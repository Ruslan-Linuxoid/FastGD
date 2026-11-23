package meta

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type Ino uint64

const RootIno Ino = 1

type Kind int

const (
	KindFile Kind = 0
	KindDir  Kind = 1

	KindHidden Kind = 2

	KindShortcut Kind = 3

	KindDoc Kind = 4
)

func listedKinds() []Kind { return []Kind{KindFile, KindDir, KindShortcut, KindDoc} }

type linkType struct {
	ext string

	open string
}

var linkTypes = map[string]linkType{
	googlePrefix + "document":     {".gdoc", "https://docs.google.com/document/d/"},
	googlePrefix + "spreadsheet":  {".gsheet", "https://docs.google.com/spreadsheets/d/"},
	googlePrefix + "presentation": {".gslides", "https://docs.google.com/presentation/d/"},
	googlePrefix + "drawing":      {".gdraw", "https://docs.google.com/drawings/d/"},
	googlePrefix + "form":         {ext: ".gform"},
	googlePrefix + "site":         {ext: ".gsite"},
	googlePrefix + "map":          {ext: ".gmap"},
	googlePrefix + "script":       {ext: ".gscript"},
	googlePrefix + "jam":          {ext: ".gjam"},
}

var otherLink = linkType{ext: ".glink"}

func linkTypeOf(mime string) linkType {
	t, known := linkTypes[mime]
	switch {
	case known:
		return t
	case mime == mimeFolder || mime == mimeShortcut || !strings.HasPrefix(mime, googlePrefix):
		return linkType{}
	}
	return otherLink
}

func (it Item) OpenURL() (string, bool) {
	base := linkTypeOf(it.Mime).open
	if base == "" {
		return "", false
	}
	return base + it.ID, true
}

func IsLinkName(name string) bool {
	for _, t := range linkTypes {
		if hasExt(name, t.ext) {
			return true
		}
	}
	return hasExt(name, otherLink.ext)
}

func hasExt(name, ext string) bool {
	return len(name) > len(ext) && strings.EqualFold(name[len(name)-len(ext):], ext)
}

func isListed(k Kind) bool { return slices.Contains(listedKinds(), k) }

var shown = func() string {
	ks := make([]string, len(listedKinds()))
	for i, k := range listedKinds() {
		ks[i] = strconv.Itoa(int(k))
	}
	return "trashed = 0 AND kind IN (" + strings.Join(ks, ", ") + ")"
}()

const (
	mimeFolder   = "application/vnd.google-apps.folder"
	mimeShortcut = "application/vnd.google-apps.shortcut"
	googlePrefix = "application/vnd.google-apps."
)

func KindOf(mime string) Kind {
	switch {
	case mime == mimeFolder:
		return KindDir
	case mime == mimeShortcut:
		return KindShortcut
	case linkTypeOf(mime).ext != "":
		return KindDoc
	}
	return KindFile
}

type Denied uint8

const (
	DenyModify Denied = 1 << iota
	DenyTrash
	DenyRename
	DenyMove
	DenyAddChildren
	DenyRemoveChildren
	DenyDownload
)

func (d Denied) Has(r Denied) bool { return d&r == r }

func (s *Store) Docs() ([]Item, error) {
	rows, err := s.r.Query(`SELECT `+itemCols+` FROM items WHERE kind = ?`, KindDoc)
	if err != nil {
		return nil, fmt.Errorf("meta: docs: %w", err)
	}
	out, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: docs: %w", err)
	}
	return out, nil
}
