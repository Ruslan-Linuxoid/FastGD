//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func requireUser(t *testing.T, pid, uid int) {
	t.Helper()
	tasks, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/task")
	if err != nil || len(tasks) == 0 {
		t.Fatalf("the threads of %d: %v", pid, err)
	}
	for _, task := range tasks {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + task.Name() + "/status")
		if err != nil {
			t.Fatal(err)
		}
		requireThread(t, pid, task.Name(), statusFields(string(data)), uid)
	}
}

func requireThread(t *testing.T, pid int, task string, ids map[string][]string, uid int) {
	t.Helper()
	if !allAre(ids["Uid"], strconv.Itoa(uid)) {
		t.Fatalf("thread %s of %d runs as %q, want %d", task, pid, ids["Uid"], uid)
	}
	if uid != 0 && (slices.Contains(ids["Gid"], "0") || slices.Contains(ids["Groups"], "0")) {
		t.Fatalf("thread %s of %d holds a group of root: %q, %q", task, pid, ids["Gid"], ids["Groups"])
	}
	if held := !noCaps(ids["CapEff"]) || !noCaps(ids["CapPrm"]); held != (uid == 0) {
		t.Fatalf("thread %s of %d, of user %d, holds the capabilities %q, permitted %q", task, pid, uid,
			ids["CapEff"], ids["CapPrm"])
	}
}

func noCaps(set []string) bool { return len(set) == 1 && strings.Trim(set[0], "0") == "" }

func statusFields(status string) map[string][]string {
	out := map[string][]string{}
	for line := range strings.SplitSeq(status, "\n") {
		if name, rest, ok := strings.Cut(line, ":"); ok {
			out[name] = strings.Fields(rest)
		}
	}
	return out
}

func allAre(ids []string, id string) bool {
	return len(ids) == 4 && !slices.ContainsFunc(ids, func(s string) bool { return s != id })
}

func childrenOf(t *testing.T, pid int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var kids []int
	for _, en := range entries {
		n, err := strconv.Atoi(en.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + en.Name() + "/stat")
		if err != nil {
			continue
		}

		f := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
		if len(f) > 1 && f[1] == strconv.Itoa(pid) {
			kids = append(kids, n)
		}
	}
	return kids
}

func cmdline(t *testing.T, pid int) string {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\x00", " ")
}
