package helper

import (
	"testing"

	"github.com/google/uuid"
)

func TestEnsureUniqueUUIDsRejectsDuplicates(t *testing.T) {
	id := uuid.New()
	if err := EnsureUniqueUUIDs([]uuid.UUID{id, id}); err == nil {
		t.Fatal("expected duplicate UUIDs to be rejected")
	}
}

func TestEnsureUniqueUUIDsAcceptsUniqueValues(t *testing.T) {
	if err := EnsureUniqueUUIDs([]uuid.UUID{uuid.New(), uuid.New()}); err != nil {
		t.Fatalf("expected unique UUIDs to be accepted: %v", err)
	}
}
