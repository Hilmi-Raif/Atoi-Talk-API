package helper

import (
	"strings"
	"testing"
)

func TestCursorRoundTripAndInvalidValues(t *testing.T) {
	cursor := EncodeCursor("created", "42", ":")
	left, right, err := DecodeCursor(cursor, ":")
	if err != nil || left != "created" || right != "42" {
		t.Fatalf("unexpected cursor result: %q %q %v", left, right, err)
	}

	for _, invalid := range []string{"not-base64", EncodeCursor("one", "two", "_")} {
		if _, _, err := DecodeCursor(invalid, ":"); err == nil {
			t.Fatalf("expected invalid cursor error for %q", invalid)
		}
	}
}

func TestEncodeCursorUsesURLSafeBase64(t *testing.T) {
	encoded := EncodeCursor("a/b", "c+d", ":")
	if strings.Contains(encoded, "+") || strings.Contains(encoded, "/") {
		t.Fatalf("cursor is not URL-safe: %q", encoded)
	}
	if _, _, err := DecodeCursor(encoded, ":"); err != nil {
		t.Fatal(err)
	}
}
