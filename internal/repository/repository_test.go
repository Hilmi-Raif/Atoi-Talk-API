package repository

import (
	"AtoiTalkAPI/internal/config"
	"context"
	"testing"
)

func TestNewRepositoryInitializesAllRepositoryBoundaries(t *testing.T) {
	repo := NewRepository(nil, nil, &config.AppConfig{})
	if repo.Chat == nil || repo.User == nil || repo.Message == nil || repo.GroupMember == nil || repo.GroupChat == nil || repo.Session == nil || repo.RateLimit == nil {
		t.Fatalf("expected all repository fields initialized: %+v", repo)
	}
	if counts, err := repo.GroupMember.CountActiveMembersByGroupIDs(context.Background()); err != nil || len(counts) != 0 {
		t.Fatalf("expected empty group count result, got counts=%v err=%v", counts, err)
	}
}
