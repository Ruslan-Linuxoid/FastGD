package mounts

import "testing"

func TestUnescape(t *testing.T) {
	for in, want := range map[string]string{
		`/plain`:         "/plain",
		`/with\040space`: "/with space",
		`/tab\011end`:    "/tab\tend",
		`/back\134slash`: `/back\slash`,
		`/short\04`:      `/short\04`,
	} {
		if got := unescape(in); got != want {
			t.Errorf("unescape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRootIsMounted(t *testing.T) {
	types, err := At("/")
	if err != nil || len(types) == 0 {
		t.Fatalf("At(/) = %q, %v", types, err)
	}
}
