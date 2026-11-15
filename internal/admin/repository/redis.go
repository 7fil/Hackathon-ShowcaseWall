package repository

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/admin/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/cache"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/metrics"
)

// RedisCache invalidates the public cache and publishes realtime events. The hub
// only reads from the channel; every write to it comes from a service.
type RedisCache struct {
	rdb *redis.Client
}

var (
	_ service.Cache          = (*RedisCache)(nil)
	_ service.EventPublisher = (*RedisCache)(nil)
)

// NewRedisCache returns the Redis-backed cache and event publisher.
func NewRedisCache(rdb *redis.Client) *RedisCache { return &RedisCache{rdb: rdb} }

// BumpVersion makes every versioned gallery key unreachable with one increment.
func (c *RedisCache) BumpVersion(ctx context.Context) error {
	return c.rdb.Incr(ctx, cache.WorksVersionKey).Err()
}

// Delete removes unversioned keys such as work:{id}.
func (c *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.rdb.Del(ctx, keys...).Err()
}

// PublishWorkEvent numbers the event from the shared counter and publishes it.
func (c *RedisCache) PublishWorkEvent(ctx context.Context, eventType string, payload any) error {
	seq, err := c.rdb.Incr(ctx, cache.WSSeqKey).Result()
	if err != nil {
		return err
	}
	b, err := json.Marshal(service.WsEvent{Type: eventType, Seq: seq, Payload: payload})
	if err != nil {
		return err
	}
	if err := c.rdb.Publish(ctx, cache.WSChannel, string(b)).Err(); err != nil {
		return err
	}
	metrics.IncWSEventsPublished()
	return nil
}
