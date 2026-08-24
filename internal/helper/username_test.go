package helper

import "testing"

func TestNormalizeUsername(t *testing.T) {
	if got := NormalizeUsername("  John.Doe-42! "); got != "johndoe42" {
		t.Fatalf("unexpected username: %q", got)
	}
}
