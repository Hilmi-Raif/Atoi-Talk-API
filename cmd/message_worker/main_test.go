package main

import (
	"testing"
	"time"

	"AtoiTalkAPI/internal/infrastructure/config"
	"github.com/stretchr/testify/require"
)

func TestMessageEventDedupeTTLMatchesOutboxRetention(t *testing.T) {
	require.Equal(t, 14*24*time.Hour, messageEventDedupeTTL(&config.AppConfig{MessageOutboxRetentionDays: 14}))
	require.Equal(t, 7*24*time.Hour, messageEventDedupeTTL(&config.AppConfig{}))
}
