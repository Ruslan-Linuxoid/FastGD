package core_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func busyWriter(env *testenv.Env, wh, rh uint64, written *atomic.Int64, stop <-chan struct{}, failed chan<- error) {
	buf := make([]byte, 1)
	for i := int64(0); ; i++ {
		select {
		case <-stop:
			failed <- nil
			return
		default:
		}
		if _, err := env.FS.Write(context.Background(), wh, i, []byte{byte(i)}); err != nil {
			failed <- fmt.Errorf("write at %d: %w", i, err)
			return
		}
		if n, err := env.FS.Read(context.Background(), rh, i, buf); err != nil || n != 1 || buf[0] != byte(i) {
			failed <- fmt.Errorf("read at %d: %d bytes, %v, %w", i, n, buf, err)
			return
		}
		written.Store(i + 1)
	}
}

func requireDriveHoldsTheEdits(t *testing.T, env *testenv.Env, doc string) {
	t.Helper()
	if f := inDrive(t, env, "b.txt"); string(f.Content) != "a" || f.ID != item(t, env, "b.txt").ID {
		t.Fatalf("Drive holds the renamed file as %q with %q", f.ID, f.Content)
	}
	if f, _ := env.Emu.Get(doc); string(f.Content) != "new" || f.Trashed {
		t.Fatalf("the file of Drive that was saved over holds %q, trashed %v", f.Content, f.Trashed)
	}
}

func requireNothingTemporaryWentToDrive(t *testing.T, env *testenv.Env) {
	t.Helper()
	for _, f := range env.Emu.All() {
		if f.Name == "gone.txt" || f.Name == "a.txt" || f.Name == "doc.tmp" {
			t.Fatalf("%q went to Drive", f.Name)
		}
	}
	if names := env.Names(""); len(names) != 2 || names[0] != "b.txt" || names[1] != "doc.txt" {
		t.Fatalf("the root holds %q", names)
	}
}
