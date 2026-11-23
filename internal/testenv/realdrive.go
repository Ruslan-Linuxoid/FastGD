//go:build realdrive

package testenv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/auth"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

const realDriveEnv = "FASTGD_REAL_DRIVE"

const googleAPI = "https://www.googleapis.com/drive/v3/"

type Real struct {
	tb testing.TB

	Drive *drive.Client

	HTTP *http.Client

	Folder, FolderName string

	Dir string
}

func RealDrive(tb testing.TB) *Real {
	tb.Helper()
	dir := os.Getenv(realDriveEnv)
	if dir == "" {
		tb.Fatal(realDriveEnv + " is not set: the tests of the real Drive run through make test-drive")
	}
	hc, err := auth.HTTPClient(tb.Context(), filepath.Join(dir, "client_id.json"), filepath.Join(dir, "token.json"),
		drive.Transport(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		tb.Fatal(err)
	}
	cl, err := drive.New(tb.Context(), drive.Options{HTTPClient: hc})
	if err != nil {
		tb.Fatal(err)
	}
	r := &Real{tb: tb, Drive: cl, HTTP: hc, Dir: dir}
	root, err := cl.RootID(tb.Context())
	if err != nil {
		tb.Fatal(err)
	}
	r.FolderName = "fastgd test " + tb.Name() + " " + time.Now().UTC().Format("2006-01-02 15-04-05")
	r.Folder = r.MakeFolder(r.FolderName, root)
	folder := r.Folder
	tb.Cleanup(func() {

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := cl.UpdateMeta(ctx, folder, drive.MetaUpdate{Trashed: true}); err != nil {
			tb.Errorf("the folder of the test %s stays out of the trash: %v", folder, err)
		}
	})
	return r
}

func (r *Real) MakeFolder(name, parent string) string {
	r.tb.Helper()
	ids, err := r.Drive.GenerateIDs(r.tb.Context(), 1)
	if err != nil {
		r.tb.Fatal(err)
	}
	f, err := r.Drive.CreateFolder(r.tb.Context(), ids[0], name, parent, time.Now())
	if err != nil {
		r.tb.Fatal(err)
	}
	return f.ID
}

func (r *Real) GetJSON(path string, q url.Values, v any) (int, string) {
	r.tb.Helper()
	return r.Call(http.MethodGet, path, q, nil, v)
}

func (r *Real) Call(method, path string, q url.Values, body, v any) (int, string) {
	r.tb.Helper()
	var in io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			r.tb.Fatal(err)
		}
		in = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(r.tb.Context(), method, googleAPI+path+"?"+q.Encode(), in)
	if err != nil {
		r.tb.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		r.tb.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	if v == nil {
		return resp.StatusCode, ""
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		r.tb.Fatal(fmt.Errorf("the answer of %s: %w", path, err))
	}
	return resp.StatusCode, ""
}
