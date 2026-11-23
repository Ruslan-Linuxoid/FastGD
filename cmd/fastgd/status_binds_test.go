package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/binds"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
)

func TestStatusShowsIgnoredBindingsAlone(t *testing.T) {
	var out bytes.Buffer
	printIgnored(&out, []binds.Row{
		{Folder: "a", Target: "/srv/kept", State: binds.Bound},
		{Folder: "b", Target: "/srv/waits", State: binds.Pending},
		{Folder: "c", Target: "/srv/gone", State: binds.Ignored, Reason: core.GoneTrash},
	})
	if want := "binding:      /srv/gone is ignored, its folder is in Drive's trash\n"; out.String() != want {
		t.Fatalf("status prints %q, want %q", out.String(), want)
	}
}

func TestStatusNamesAListOfBindingsItCannotRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mounts.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printBindingNotes(&out, dir)
	if got := out.String(); !strings.HasPrefix(got, "bindings:     the list of bindings "+filepath.Join(dir,
		"mounts.json")+" cannot be read") || !strings.Contains(got, "delete the file") {
		t.Fatalf("status prints %q", got)
	}
}
