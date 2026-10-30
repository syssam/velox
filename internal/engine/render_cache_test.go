package engine

import (
	"testing"
)

func TestFingerprintFormat(t *testing.T) {
	// SHA-256 of the empty string starts e3b0c44298fc1c14.
	if got := Fingerprint(""); got != "e3b0c44298fc1c14" {
		t.Errorf("fingerprint(\"\") = %s", got)
	}
}
