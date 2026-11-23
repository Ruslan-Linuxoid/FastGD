package core_test

import (
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestTheAddressOfALinkPageIsEscaped(t *testing.T) {
	t.Parallel()
	page := string(core.LinkPage(meta.Item{ID: `a"><b&c`, Mime: mimeDoc}))
	want := "https://docs.google.com/document/d/a&#34;&gt;&lt;b&amp;c"
	if strings.Contains(page, `a"><b`) || strings.Count(page, want) != 3 {
		t.Fatalf("the page is %q, want the address escaped as %q in the refresh, the link and its text", page, want)
	}
}

func TestALinkPageSendsTheBrowserToTheDocument(t *testing.T) {
	t.Parallel()
	for mime, want := range map[string]string{
		mimeGoogle + "form":   "https://docs.google.com/forms/d/x/edit",
		mimeGoogle + "site":   "https://sites.google.com/d/x/edit",
		mimeGoogle + "script": "https://script.google.com/d/x/edit",
		mimeGoogle + "jam":    "https://drive.google.com/open?id=x",
		mimeGoogle + "vid":    "https://drive.google.com/open?id=x",
		mimeSheet:             "https://docs.google.com/spreadsheets/d/x",
	} {
		if page := string(core.LinkPage(meta.Item{ID: "x", Mime: mime})); !strings.Contains(page, `url=`+want+`"`) {
			t.Fatalf("the page of %s is %q, want it to send to %s", mime, page, want)
		}
	}
}
