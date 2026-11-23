package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestARefusedSignInStopsTheNetwork(t *testing.T) {
	t.Parallel()
	mountSignedIn(t, func(m *signedIn) {
		if logs := m.revoked(t); len(logs) != 1 || !strings.HasPrefix(logs[0], "ERROR") {
			t.Errorf("the refusal is told by %q, want one error", logs)
		}
		m.g.count()
		time.Sleep(15 * time.Minute)
		if n := m.g.count(); n != 0 {
			t.Errorf("Google got %d requests in 15 minutes after it refused the sign-in, want none", n)
		}
		if logs := m.log.take(); len(logs) != 0 {
			t.Errorf("the log tells %q after the refusal", logs)
		}
		if m.uploaded("new.txt") {
			t.Error("new.txt went to Drive without a sign-in")
		}
	})
}

func TestANewSignInIsTakenWithoutARestart(t *testing.T) {
	t.Parallel()
	mountSignedIn(t, func(m *signedIn) {
		m.revoked(t)
		m.g.mu.Lock()
		m.g.refresh["r2"] = true
		m.g.mu.Unlock()
		m.signIn(t, "r2", "you@example.com")
		time.Sleep(time.Minute)
		if !m.uploaded("new.txt") {
			t.Error("a minute after the new sign-in new.txt is not in Drive")
		}
		if _, needed, _ := m.fs.Store().GetState(meta.StateLoginNeeded); needed {
			t.Error("after the new sign-in status still tells that one is needed")
		}
	})
}

func TestASignInOfAnotherAccountIsNotTaken(t *testing.T) {
	t.Parallel()
	mountSignedIn(t, func(m *signedIn) {
		m.revoked(t)
		m.g.mu.Lock()
		m.g.refresh["r2"] = true
		m.g.mu.Unlock()
		m.signIn(t, "r2", "other@example.com")
		m.g.count()
		time.Sleep(5 * time.Minute)
		if n := m.g.count(); n != 0 {
			t.Errorf("Google got %d requests with the sign-in of another account", n)
		}
		if logs := m.log.take(); len(logs) != 1 || !strings.Contains(logs[0], "another account") {
			t.Errorf("the sign-in of another account is told by %q, want one error", logs)
		}
		if m.uploaded("new.txt") {
			t.Error("new.txt went to Drive with the sign-in of another account")
		}
	})
}

func TestARenewalDriveRefusesStopsTheNetwork(t *testing.T) {
	t.Parallel()
	mountSignedIn(t, func(m *signedIn) {
		m.g.mu.Lock()
		m.g.refuses = true
		m.g.mu.Unlock()
		time.Sleep(time.Minute)
		if _, needed, _ := m.fs.Store().GetState(meta.StateLoginNeeded); !needed {
			t.Fatal("status does not tell that Drive refuses the sign-in")
		}
		m.g.count()
		time.Sleep(15 * time.Minute)
		if n := m.g.count(); n != 0 {
			t.Errorf("Google got %d requests in 15 minutes after Drive refused the renewed token, want none", n)
		}
	})
}

func TestTwoCurable401sKeepTheNetwork(t *testing.T) {
	t.Parallel()
	mountSignedIn(t, func(m *signedIn) {
		for _, name := range []string{"fa.txt", "fb.txt"} {
			m.g.mu.Lock()
			m.g.access = map[string]bool{}
			m.g.mu.Unlock()
			m.write(t, name)
			time.Sleep(12 * time.Minute)
			if !m.uploaded(name) {
				t.Errorf("%s is not in Drive after a 401 a renewal cures", name)
			}
		}
		if _, needed, _ := m.fs.Store().GetState(meta.StateLoginNeeded); needed {
			t.Error("status tells a refusal of the sign-in for 401s a renewal cures")
		}
		for _, l := range m.log.take() {
			if strings.HasPrefix(l, "ERROR") {
				t.Errorf("the log tells %q for 401s a renewal cures", l)
			}
		}
	})
}

func TestARefusalByForbiddenStopsTheNetwork(t *testing.T) {
	t.Parallel()
	mountSignedIn(t, func(m *signedIn) {
		m.g.mu.Lock()
		m.g.forbids = true
		m.g.mu.Unlock()
		m.revoked(t)
		m.g.count()
		time.Sleep(15 * time.Minute)
		if n := m.g.count(); n != 0 {
			t.Errorf("Google got %d requests in 15 minutes after it refused the sign-in, want none", n)
		}
	})
}
