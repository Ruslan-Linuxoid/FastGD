package drive_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func changesClient(t *testing.T, emu *emulator.Emulator, log io.Writer) *drive.Client {
	t.Helper()
	cl, err := drive.New(context.Background(), drive.Options{BaseURL: "http://emulator",
		HTTPClient: &http.Client{Transport: emulator.InProcess(emu)},
		Log:        slog.New(slog.NewTextHandler(log, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func sizesAsked(emu *emulator.Emulator, cl *drive.Client, token string, code int, reason string) ([]string, error) {
	emu.ResetRequests()
	if code != 0 {
		emu.FailRequests(1, code, reason, 0)
	}
	_, _, err := cl.Changes(context.Background(), token)
	var got []string
	for _, r := range emu.Requests() {
		if r.Path == "/drive/v3/changes" {
			got = append(got, r.Query.Get("pageSize"))
		}
	}
	return got, err
}

func startToken(t *testing.T, cl *drive.Client) string {
	t.Helper()
	token, err := cl.StartPageToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestThePageOfChangesShrinksOn5xxAndGrowsBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		emu := emulator.New()
		var logs bytes.Buffer
		cl := changesClient(t, emu, &logs)
		token := startToken(t, cl)
		pages := func(when string, fail bool, want ...string) {
			t.Helper()
			code := 0
			if fail {
				code = http.StatusServiceUnavailable
			}
			got, err := sizesAsked(emu, cl, token, code, "backendError")
			if err != nil {
				t.Fatalf("%s: %v", when, err)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("%s the pages asked for are %q, want %q", when, got, want)
			}
		}
		pages("after an answer 503", true, "1000", "500")
		time.Sleep(time.Minute)
		pages("after a second 503 within five minutes", true, "500", "500")
		time.Sleep(9 * time.Minute)
		pages("ten minutes after the size went down", false, "500")
		pages("after an answer that passed ten minutes on", false, "1000")
		cl = changesClient(t, emu, &logs)
		pages("with a new client", true, "1000", "500")
		if n := strings.Count(logs.String(), "the page of changes asked for changes its size"); n != 3 {
			t.Fatalf("the log tells %d changes of the size, want 3:\n%s", n, logs.String())
		}
	})
}

func TestThePageOfChangesGoesNoLowerThanOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		emu := emulator.New()
		cl := changesClient(t, emu, io.Discard)
		token := startToken(t, cl)
		before := "1000"
		for _, after := range []string{"500", "250", "125", "62", "31", "15", "7", "3", "1", "1", "1"} {
			got, err := sizesAsked(emu, cl, token, http.StatusServiceUnavailable, "backendError")
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{before, after}; !slices.Equal(got, want) {
				t.Fatalf("on an answer 503 the pages asked for are %q, want %q", got, want)
			}
			before = after
			time.Sleep(5 * time.Minute)
		}
	})
}

func TestAnAnswer4xxLeavesThePageOfChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		emu := emulator.New()
		cl := changesClient(t, emu, io.Discard)
		token := startToken(t, cl)
		if _, err := sizesAsked(emu, cl, token, http.StatusServiceUnavailable, "backendError"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Minute)
		for _, c := range []struct {
			code   int
			reason string
		}{{http.StatusUnauthorized, "authError"}, {http.StatusBadRequest, "badRequest"}, {0, ""}} {
			got, err := sizesAsked(emu, cl, token, c.code, c.reason)
			if !slices.Equal(got, []string{"500"}) {
				t.Fatalf("after the answer before %d the pages asked for are %q, want 500", c.code, got)
			}
			if (err == nil) != (c.code == 0) {
				t.Fatalf("the ask answered %d gave the error %v", c.code, err)
			}
		}
	})
}

func TestThePaceOfGoingDownCountsFromTheLastFall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		emu := emulator.New()
		cl := changesClient(t, emu, io.Discard)
		token := startToken(t, cl)
		for _, s := range []struct {
			wait time.Duration
			code int
			want []string
		}{
			{0, http.StatusServiceUnavailable, []string{"1000", "500"}},
			{10 * time.Minute, 0, []string{"500"}},
			{0, 0, []string{"1000"}},
			{time.Minute, http.StatusServiceUnavailable, []string{"1000", "500"}},
		} {
			time.Sleep(s.wait)
			got, err := sizesAsked(emu, cl, token, s.code, "backendError")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, s.want) {
				t.Fatalf("%v on, answered %d, the pages asked for are %q, want %q", s.wait, s.code, got, s.want)
			}
		}
	})
}
