package helper

import (
	"fmt"

	"github.com/google/uuid"
)

func EnsureUniqueUUIDs(ids []uuid.UUID) error {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate UUID: %s", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}
