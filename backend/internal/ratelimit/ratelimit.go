package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb}
}

func (l *Limiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, time.Duration, error) {
	k := "rl:" + key
	pipe := l.rdb.TxPipeline()
	pipe.SetNX(ctx, k, 0, window)
	count := pipe.Incr(ctx, k)
	ttl := pipe.PTTL(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, err
	}
	retry := ttl.Val()
	if retry < time.Second {
		retry = time.Second
	}
	return count.Val() <= limit, retry, nil
}
