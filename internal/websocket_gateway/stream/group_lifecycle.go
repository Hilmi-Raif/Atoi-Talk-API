package stream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	groupLeaseTTL        = 5 * time.Minute
	groupRefreshInterval = time.Minute
	groupCleanupLockTTL  = 30 * time.Second
)

var ErrGroupLeaseOwned = errors.New("realtime stream group lease is owned by another process")

var refreshGroupLeaseScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner and owner ~= ARGV[1] then
  return 0
end
redis.call('PSETEX', KEYS[1], ARGV[2], ARGV[1])
return 1
`)

var closeOwnedGroupScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
if redis.call('EXISTS', KEYS[2]) ~= 0 then
  redis.call('XGROUP', 'DESTROY', KEYS[2], ARGV[2])
end
redis.call('DEL', KEYS[1])
return 1
`)

var resetOwnedGroupScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
if redis.call('EXISTS', KEYS[2]) ~= 0 then
  redis.call('XGROUP', 'DESTROY', KEYS[2], ARGV[2])
end
return 1
`)

var destroyStaleGroupScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) ~= 0 then
  return 0
end
return redis.call('XGROUP', 'DESTROY', KEYS[2], ARGV[1])
`)

var releaseGroupCleanupLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var refreshGroupCleanupLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)

type GroupLifecycle struct {
	client redis.Scripter
	redis  *redis.Client
	stream string
	group  string
	prefix string
	token  string
}

func NewGroupLifecycle(client *redis.Client, stream, group, prefix string) *GroupLifecycle {
	return &GroupLifecycle{
		client: client,
		redis:  client,
		stream: stream,
		group:  group,
		prefix: strings.TrimSpace(prefix),
		token:  uuid.NewString(),
	}
}

func (l *GroupLifecycle) RefreshLease(ctx context.Context) error {
	result, err := refreshGroupLeaseScript.Run(ctx, l.client, []string{l.leaseKey(l.group)}, l.token, groupLeaseTTL.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("refresh realtime stream group lease: %w", err)
	}
	if result == 0 {
		return ErrGroupLeaseOwned
	}
	return nil
}

func (l *GroupLifecycle) CleanupStale(ctx context.Context) error {
	lockKey := fmt.Sprintf("realtime:stream-group:cleanup:%s:%s", l.stream, l.prefix)
	lockToken := uuid.NewString()
	locked, err := l.redis.SetNX(ctx, lockKey, lockToken, groupCleanupLockTTL).Result()
	if err != nil {
		return fmt.Errorf("acquire realtime stream group cleanup lock: %w", err)
	}
	if !locked {
		return nil
	}
	defer func() {
		if err := releaseGroupCleanupLockScript.Run(context.WithoutCancel(ctx), l.client, []string{lockKey}, lockToken).Err(); err != nil {
			slog.WarnContext(ctx, "failed to release realtime stream group cleanup lock", "error", err)
		}
	}()

	groups, err := l.redis.XInfoGroups(ctx, l.stream).Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return nil
		}
		return fmt.Errorf("list realtime stream groups: %w", err)
	}
	for _, group := range groups {
		refreshed, err := refreshGroupCleanupLockScript.Run(ctx, l.client, []string{lockKey}, lockToken, groupCleanupLockTTL.Milliseconds()).Int()
		if err != nil {
			return fmt.Errorf("refresh realtime stream group cleanup lock: %w", err)
		}
		if refreshed == 0 {
			return nil
		}
		if !strings.HasPrefix(group.Name, l.prefix+"-") {
			continue
		}
		if err := destroyStaleGroupScript.Run(ctx, l.client, []string{l.leaseKey(group.Name), l.stream}, group.Name).Err(); err != nil {
			return fmt.Errorf("destroy stale realtime stream group %s: %w", group.Name, err)
		}
	}
	return nil
}

func (l *GroupLifecycle) ResetGroup(ctx context.Context) error {
	result, err := resetOwnedGroupScript.Run(ctx, l.client, []string{l.leaseKey(l.group), l.stream}, l.token, l.group).Int()
	if err != nil {
		return fmt.Errorf("reset realtime stream group: %w", err)
	}
	if result == 0 {
		return ErrGroupLeaseOwned
	}
	return nil
}

func (l *GroupLifecycle) Run(ctx context.Context) error {
	ticker := time.NewTicker(groupRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := l.RefreshLease(ctx); err != nil {
				if errors.Is(err, ErrGroupLeaseOwned) {
					return err
				}
				slog.WarnContext(ctx, "failed to refresh realtime stream group lease", "error", err)
				continue
			}
			if err := l.CleanupStale(ctx); err != nil {
				slog.WarnContext(ctx, "failed to clean stale realtime stream groups", "error", err)
			}
		}
	}
}

func (l *GroupLifecycle) Close(ctx context.Context) error {
	if err := closeOwnedGroupScript.Run(ctx, l.client, []string{l.leaseKey(l.group), l.stream}, l.token, l.group).Err(); err != nil {
		return fmt.Errorf("remove realtime stream group: %w", err)
	}
	return nil
}

func (l *GroupLifecycle) leaseKey(group string) string {
	return fmt.Sprintf("realtime:stream-group:lease:%s:%s", l.stream, group)
}
