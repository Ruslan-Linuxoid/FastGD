//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func TestTheBindingOfAFolderGoneIsIgnored(t *testing.T) {
	for name, gone := range map[string]func(e *emu, docs string){
		"deleted": func(e *emu, docs string) { e.Delete(docs) },
		"left My Drive": func(e *emu, docs string) {
			e.TellParentless(emulator.ParentlessRemoved)
			e.Update(docs, func(f *emulator.File) { f.Parents = nil })
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := startEmu(t)
			docs := workDocs(e)
			d := newDaemon(t, e)
			d.start()
			at := places(t, d)
			d.mustMount("add", "My Drive/Work/Docs", at+"/docs")
			d.stop()
			d.start()
			requireA(t, at+"/docs/a.txt")
			gone(e, docs)
			waitFor(t, "the binding is undone", 20*time.Second, func() bool { return !mounted(at + "/docs") })
			if out := d.mustMount("list"); !strings.Contains(out, "ignored: deleted in Drive, or gone from My Drive") {
				t.Fatalf("the list is %q", out)
			}
		})
	}
}
