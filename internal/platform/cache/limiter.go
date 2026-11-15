package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter is a fixed-window counter in Redis. Keys are prefixed with "rl:".
type Limiter struct {
	rdb *redis.Client
}

func NewLimiter(rdb *redis.Client) *Limiter { return &Limiter{rdb: rdb} }

// The window TTL is set in the same script as the first increment, so a crash
// can never leave a counter without an expiry.
var hitScript = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('PTTL', KEYS[1])
return {c, ttl}
`)

// Allow counts one hit and reports whether it is within limit for the window.
// When it is not, retryAfter is the time left in the current window.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	res, err := hitScript.Run(ctx, l.rdb, []string{"rl:" + key}, window.Milliseconds()).Slice()
	if err != nil {
		return false, 0, err
	}
	count, _ := res[0].(int64)
	ttlMs, _ := res[1].(int64)
	if int(count) > limit {
		return false, time.Duration(ttlMs) * time.Millisecond, nil
	}
	return true, 0, nil
}

// Count returns the current counter and the time left in its window without counting a hit.
func (l *Limiter) Count(ctx context.Context, key string) (int, time.Duration, error) {
	pipe := l.rdb.Pipeline()
	get := pipe.Get(ctx, "rl:"+key)
	ttl := pipe.PTTL(ctx, "rl:"+key)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, 0, err
	}
	n, err := get.Int()
	if err == redis.Nil {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	d, _ := ttl.Result()
	if d < 0 {
		d = 0
	}
	return n, d, nil
}

func (l *Limiter) Reset(ctx context.Context, key string) error {
	return l.rdb.Del(ctx, "rl:"+key).Err()
}
