package core

import (
	"context"
	"html"
	"strings"
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const (
	xattrPrefix = "user.drive."

	xattrDoc = xattrPrefix + "gdoc"

	xattrOpenURL = xattrPrefix + "item_open_url"

	xattrEmail = xattrPrefix + "email"
)

func (f *FS) email() string {
	email, _, err := f.store.GetState(meta.StateEmail)
	if err != nil {
		f.log.Warn("read the account's email", "err", err)
	}
	return email
}

func (f *FS) ListXattr(ino uint64) ([]string, error) {
	it, err := f.item(ino)
	if err != nil || it.Kind != meta.KindDoc {
		return nil, err
	}
	names := []string{xattrDoc, xattrOpenURL}
	if f.email() != "" {
		names = append(names, xattrEmail)
	}
	return names, nil
}

func (f *FS) GetXattr(ctx context.Context, ino uint64, name string) (string, error) {
	if !strings.HasPrefix(name, xattrPrefix) {
		return "", syscall.ENOTSUP
	}
	it, err := f.item(ino)
	if err != nil {
		return "", err
	}
	if it.Kind != meta.KindDoc {
		return "", syscall.ENOTSUP
	}
	switch name {
	case xattrDoc:
		return "1", nil
	case xattrEmail:
		if email := f.email(); email != "" {
			return email, nil
		}
	case xattrOpenURL:
		return f.openURLXattr(ctx, it)
	}
	return "", syscall.ENODATA
}

func (f *FS) openURLXattr(ctx context.Context, it meta.Item) (string, error) {
	if url, ok := it.OpenURL(); ok {
		return url, nil
	}
	rf, err := f.opt.Drive.Get(ctx, it.ID)
	if err != nil && ctx.Err() != nil {

		return "", syscall.EINTR
	}
	if err != nil {
		return "", f.eio("the address of a document", err, "id", it.ID, "name", it.Name)
	}
	if rf.ViewLink == "" {

		return "", syscall.ENODATA
	}
	return rf.ViewLink, nil
}

const openURLPage = "https://drive.google.com/open?id="

var pageURLs = map[string][2]string{
	"application/vnd.google-apps.form":   {"https://docs.google.com/forms/d/", pageEdit},
	"application/vnd.google-apps.site":   {"https://sites.google.com/d/", pageEdit},
	"application/vnd.google-apps.script": {"https://script.google.com/d/", pageEdit},
}

const pageEdit = "/edit"

func linkPage(it meta.Item) []byte {
	url, ok := it.OpenURL()
	if page, known := pageURLs[it.Mime]; !ok && known {
		url, ok = page[0]+it.ID+page[1], true
	}
	if !ok {
		url = openURLPage + it.ID
	}
	u := html.EscapeString(url)
	return []byte(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0; url=` + u + `">
</head><body><a href="` + u + `">` + u + `</a></body></html>
`)
}

func (f *FS) openLink(it meta.Item, flags uint32) (uint64, error) {
	if isWrite(flags) || flags&syscall.O_TRUNC != 0 {
		return 0, syscall.EACCES
	}
	return f.handles.add(&handle{ino: it.Ino, page: linkPage(it)}), nil
}

func readPage(page []byte, off int64, dest []byte) int {
	if off >= int64(len(page)) {
		return 0
	}
	return copy(dest, page[off:])
}

func (f *FS) dropExports() error {
	if _, pending, err := f.store.GetState(meta.StateDropExports); err != nil || !pending {
		return err
	}
	docs, err := f.store.Docs()
	if err != nil {
		return err
	}
	for _, it := range docs {
		if err := f.cache.Remove(it.Ino, it.ID); err != nil {
			return err
		}
	}
	f.log.Info("dropped the cached exports of documents", "documents", len(docs))
	return f.store.DeleteState(meta.StateDropExports)
}
