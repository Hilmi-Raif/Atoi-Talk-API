package fcm

import (
	"context"
	"fmt"
	"time"
)

type Sender interface {
	Send(context.Context, string, map[string]string) error
}
type SendError struct {
	Status         int
	Invalid, Retry bool
	After          time.Duration
}

func (e *SendError) Error() string { return fmt.Sprintf("FCM returned status %d", e.Status) }
