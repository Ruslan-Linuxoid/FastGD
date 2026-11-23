package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestDrainWaitsForWhatIsUnderWayAndStartsNothing(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx, stop := context.WithCancel(context.Background())
	ran := make(chan struct{})
	go func() {
		env.FS.Run(ctx)
		close(ran)
	}()
	t.Cleanup(func() {
		stop()
		<-ran
	})
	release := env.Emu.HoldUploads()
	writeFile(t, env, "", "under way.txt", []byte("under way"))
	waitUntil(t, func() bool { return env.Emu.UploadsWaiting() == 1 })
	if env.FS.Drain(100 * time.Millisecond) {
		t.Fatal("the stop told that all ended while Drive holds an upload")
	}
	writeFile(t, env, "", "later.txt", []byte("later"))
	drained := make(chan bool)
	go func() { drained <- env.FS.Drain(10 * time.Second) }()
	release()
	if !<-drained {
		t.Fatal("the stop did not see the upload end")
	}
	if f := inDrive(t, env, "under way.txt"); string(f.Content) != "under way" {
		t.Fatalf("the upload under way at the stop reached Drive as %q", f.Content)
	}

	time.Sleep(2500 * time.Millisecond)
	if _, ok := env.Emu.ByPath("later.txt"); ok {
		t.Fatal("an upload started after the stop began")
	}
	if it := item(t, env, "later.txt"); !it.Dirty {
		t.Fatal("the file written during the stop does not wait for Drive")
	}
}
