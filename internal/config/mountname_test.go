package config

import "testing"

func TestTheMountFolderOfGmailDropsTheDomain(t *testing.T) {
	for email, want := range map[string]string{
		"a@gmail.com":      "/in/FastGD - a",
		"a@GMail.com":      "/in/FastGD - a",
		"a@googlemail.com": "/in/FastGD - a",
		"a@company.com":    "/in/FastGD - a@company.com",
		"a@notgmail.com":   "/in/FastGD - a@notgmail.com",
		"a@gmail.com.evil": "/in/FastGD - a@gmail.com.evil",
	} {
		if got := MountFolder("/in", email); got != want {
			t.Errorf("MountFolder(%q) = %q, want %q", email, got, want)
		}
	}
}
