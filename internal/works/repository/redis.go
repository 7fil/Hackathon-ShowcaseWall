package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/cache"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/metrics"
)

type RedisCache struct {
	rdb *redis.Client
}

func NewRedisCache(rdb *redis.Client) *RedisCache {
	return &RedisCache{rdb: rdb}
}

func (c *RedisCache) Version(ctx context.Context) (int64, error) {
	v, err := c.rdb.Get(ctx, cache.WorksVersionKey).Int64()
	if errors.Is(err, redis.Nil) {
		return 1, nil
	}
	return v, err
}

func (c *RedisCache) BumpVersion(ctx context.Context) error {
	return c.rdb.Incr(ctx, cache.WorksVersionKey).Err()
}

func (c *RedisCache) GetJSON(ctx context.Context, key string, dest any) (bool, error) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		metrics.IncCacheRequest("miss")
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return false, err
	}
	metrics.IncCacheRequest("hit")
	return true, nil
}

func (c *RedisCache) SetJSON(ctx context.Context, key string, val any, ttl time.Duration) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, b, ttl).Err()
}

func (c *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.rdb.Del(ctx, keys...).Err()
}

func (c *RedisCache) PublishWorkEvent(ctx context.Context, eventType string, payload any) error {
	seq, err := c.rdb.Incr(ctx, cache.WSSeqKey).Result()
	if err != nil {
		return err
	}
	env := service.WsEvent{
		Type:    eventType,
		Seq:     seq,
		Payload: payload,
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := c.rdb.Publish(ctx, cache.WSChannel, string(b)).Err(); err != nil {
		return err
	}
	metrics.IncWSEventsPublished()
	return nil
}
