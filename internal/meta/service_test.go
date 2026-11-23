package meta

import "testing"

func TestServiceNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		".~lock.a.odt#":      true,
		".~lock.a.odt":       false,
		"x.~lock.a.odt#":     false,
		".a.txt.swp":         true,
		"notes.swp":          true,
		"notes.SWP":          false,
		"notes.swp.txt":      false,
		"b.iso.crdownload":   true,
		"b.iso":              false,
		"desktop.ini":        true,
		"Desktop.INI":        true,
		"my desktop.ini":     false,
		"Thumbs.db":          true,
		"EFS12.tmp":          true,
		"efs.tmp":            false,
		"report~RF1a2B.TMP":  true,
		"report~rf1a2b.tmp":  true,
		"report~RFxyz.TMP":   false,
		".DS_Store":          true,
		".ds_store":          true,
		"Icon\r":             true,
		"icon\r":             true,
		"Icon":               false,
		"a.odt":              false,
		"":                   false,
		"desktop.ini.backup": false,
	} {
		if got := isServiceName(name); got != want {
			t.Errorf("isServiceName(%q) = %v, want %v", name, got, want)
		}
	}
}
