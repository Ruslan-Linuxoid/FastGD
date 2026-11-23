//go:build realdrive

package drive_test

import (
	"bytes"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

type capabilities struct {
	CanModifyContent       *bool `json:"canModifyContent"`
	CanEdit                *bool `json:"canEdit"`
	CanAddChildren         *bool `json:"canAddChildren"`
	CanRemoveChildren      *bool `json:"canRemoveChildren"`
	CanAddMyDriveParent    *bool `json:"canAddMyDriveParent"`
	CanRemoveMyDriveParent *bool `json:"canRemoveMyDriveParent"`
}

const rightFields = "capabilities(canModifyContent,canEdit,canAddChildren,canRemoveChildren," +
	"canAddMyDriveParent,canRemoveMyDriveParent),contentRestrictions"

type item struct {
	ID           string        `json:"id"`
	Parents      []string      `json:"parents"`
	Capabilities *capabilities `json:"capabilities"`
}

func TestRealRightsOfOwnItems(t *testing.T) {
	r := testenv.RealDrive(t)
	start, err := r.Drive.StartPageToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	file := makeFile(t, r, "rights.txt", r.Folder)

	var got item
	if code, body := r.GetJSON("files/"+file, url.Values{"fields": {"id,parents," + rightFields}}, &got); code != 200 {
		t.Fatalf("files.get answered %d: %s", code, body)
	}
	t.Logf("files.get of an own file: %s", show(got.Capabilities))
	requireModify(t, "files.get", got.Capabilities)

	var list struct{ Files []item }
	q := url.Values{"q": {"'" + r.Folder + "' in parents"}, "fields": {"files(id," + rightFields + ")"}}
	if code, body := r.GetJSON("files", q, &list); code != 200 || len(list.Files) != 1 {
		t.Fatalf("files.list answered %d with %d files: %s", code, len(list.Files), body)
	}
	requireModify(t, "files.list", list.Files[0].Capabilities)

	requireModify(t, "changes.list", changedRights(t, r, start, file))

	var folder item
	if code, body := r.GetJSON("files/"+r.Folder, url.Values{"fields": {"id," + rightFields}}, &folder); code != 200 {
		t.Fatalf("files.get of the folder answered %d: %s", code, body)
	}
	t.Logf("files.get of an own folder: %s", show(folder.Capabilities))

	other := r.MakeFolder("other", r.Folder)
	move := drive.MetaUpdate{OldParent: r.Folder, NewParent: other}
	if _, err := r.Drive.UpdateMeta(t.Context(), file, move); err != nil {
		t.Fatalf("a move of an own file between two own folders: %v", err)
	}
}

func makeFile(t *testing.T, r *testenv.Real, name, parent string) string {
	t.Helper()
	ids, err := r.Drive.GenerateIDs(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("content of " + name)
	f, err := r.Drive.CreateFile(t.Context(), ids[0], name, parent, "text/plain", time.Now(), int64(len(content)),
		func() io.Reader { return bytes.NewReader(content) })
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func changedRights(t *testing.T, r *testenv.Real, start, id string) *capabilities {
	t.Helper()
	for range 6 {
		var page struct {
			Changes []struct {
				FileID  string `json:"fileId"`
				Removed bool   `json:"removed"`
				File    *item  `json:"file"`
			}
		}
		q := url.Values{"pageToken": {start}, "fields": {"changes(fileId,removed,file(id,parents," + rightFields + "))"}}
		if code, body := r.GetJSON("changes", q, &page); code != 200 {
			t.Fatalf("changes.list answered %d: %s", code, body)
		}
		for _, c := range page.Changes {
			if c.FileID == id && c.File != nil {
				return c.File.Capabilities
			}
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("the changes after %s hold no change of %s", start, id)
	return nil
}

func requireModify(t *testing.T, where string, c *capabilities) {
	t.Helper()
	if c == nil || c.CanModifyContent == nil || !*c.CanModifyContent {
		t.Errorf("%s gives an own file the rights %s, want canModifyContent true", where, show(c))
	}
}

func show(c *capabilities) string {
	if c == nil {
		return "no capabilities"
	}
	b := func(v *bool) string {
		if v == nil {
			return "-"
		}
		if *v {
			return "true"
		}
		return "false"
	}
	return "modify " + b(c.CanModifyContent) + " edit " + b(c.CanEdit) + " addChildren " + b(c.CanAddChildren) +
		" removeChildren " + b(c.CanRemoveChildren) + " addMyDriveParent " + b(c.CanAddMyDriveParent) +
		" removeMyDriveParent " + b(c.CanRemoveMyDriveParent)
}
