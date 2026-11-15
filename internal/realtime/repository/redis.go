// Package repository implements the Redis pub/sub subscriber for the realtime hub.
package repository

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/realtime/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/cache"
)

type RedisSubscriber struct {
	rdb *redis.Client
}

var _ service.Subscriber = (*RedisSubscriber)(nil)

func NewRedisSubscriber(rdb *redis.Client) *RedisSubscriber {
	return &RedisSubscriber{rdb: rdb}
}

func (s *RedisSubscriber) Subscribe(ctx context.Context, onMessage func(raw []byte)) error {
	pubsub := s.rdb.Subscribe(ctx, cache.WSChannel)
	defer pubsub.Close()

	if _, err := pubsub.Receive(ctx); err != nil {
		return err
	}
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			onMessage([]byte(msg.Payload))
		}
	}
}
