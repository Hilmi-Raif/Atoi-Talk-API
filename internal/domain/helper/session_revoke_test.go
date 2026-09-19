package helper

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type sessionRevokerFake struct {
	snapshot       SessionRevokeSnapshot
	snapshotErr    error
	revokeMarker   string
	revokeErr      error
	rollbackErr    error
	rollbackCalled bool
}

func (f *sessionRevokerFake) SnapshotUserRevoke(context.Context, uuid.UUID) (SessionRevokeSnapshot, error) {
	return f.snapshot, f.snapshotErr
}

func (f *sessionRevokerFake) RevokeAllSessionsAt(context.Context, uuid.UUID, int64) (string, error) {
	return f.revokeMarker, f.revokeErr
}

func (f *sessionRevokerFake) RollbackUserRevoke(context.Context, uuid.UUID, string, SessionRevokeSnapshot) error {
	f.rollbackCalled = true
	return f.rollbackErr
}

func TestRevokeSessionsForTransaction(t *testing.T) {
	snapshot := SessionRevokeSnapshot{Exists: true, Value: "old", TTL: time.Minute}
	fake := &sessionRevokerFake{snapshot: snapshot, revokeMarker: "marker"}

	marker, gotSnapshot, err := RevokeSessionsForTransaction(context.Background(), fake, uuid.New())
	if err != nil || marker != "marker" || gotSnapshot != snapshot {
		t.Fatalf("unexpected revoke result: marker=%q snapshot=%+v err=%v", marker, gotSnapshot, err)
	}
}

func TestRevokeSessionsForTransactionReturnsOperationErrors(t *testing.T) {
	wantErr := errors.New("failed")
	fake := &sessionRevokerFake{snapshotErr: wantErr}
	if _, _, err := RevokeSessionsForTransaction(context.Background(), fake, uuid.New()); !errors.Is(err, wantErr) {
		t.Fatalf("expected snapshot error, got %v", err)
	}

	fake = &sessionRevokerFake{revokeErr: wantErr}
	if _, _, err := RevokeSessionsForTransaction(context.Background(), fake, uuid.New()); !errors.Is(err, wantErr) {
		t.Fatalf("expected revoke error, got %v", err)
	}
}

func TestRollbackSessionRevokeIfNeeded(t *testing.T) {
	fake := &sessionRevokerFake{}
	RollbackSessionRevokeIfNeeded(fake, uuid.New(), "", SessionRevokeSnapshot{})
	if fake.rollbackCalled {
		t.Fatal("rollback should not run without an expected marker")
	}

	RollbackSessionRevokeIfNeeded(fake, uuid.New(), "marker", SessionRevokeSnapshot{})
	if !fake.rollbackCalled {
		t.Fatal("expected rollback call")
	}

	fake = &sessionRevokerFake{rollbackErr: errors.New("rollback failed")}
	RollbackSessionRevokeIfNeeded(fake, uuid.New(), "marker", SessionRevokeSnapshot{})
	if !fake.rollbackCalled {
		t.Fatal("expected rollback attempt even when it returns an error")
	}
}
