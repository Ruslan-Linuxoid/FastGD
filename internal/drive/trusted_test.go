package drive

import "testing"

func TestAddressFromAnAnswerIsOfDrive(t *testing.T) {
	t.Parallel()
	c := &Client{uploadBase: "http://127.0.0.1:4010"}
	for address, want := range map[string]bool{
		"http://127.0.0.1:4010/upload/drive/v3/files?upload_id=1":      true,
		"https://www.googleapis.com/upload/drive/v3/files?upload_id=1": true,
		"https://doc-0s-1c-docs.googleusercontent.com/x":               true,
		"https://googleapis.com/x":                                     true,
		"http://www.googleapis.com/x":                                  false,
		"https://evilgoogleapis.com/x":                                 false,
		"https://www.googleapis.com.evil.example/x":                    false,
		"http://127.0.0.1:4011/x":                                      false,
		"/relative":                                                    false,
		"":                                                             false,
	} {
		if got := c.trusted(address); got != want {
			t.Errorf("%q is trusted: %v, want %v", address, got, want)
		}
	}
}
