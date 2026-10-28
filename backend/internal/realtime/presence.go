package realtime

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	presencePrefix = "rt:presence:"
	presenceTTL    = 75 * time.Second
)

type Presence struct {
	rdb *redis.Client
	now func() time.Time
}

func NewPresence(rdb *redis.Client) *Presence {
	return &Presence{rdb: rdb, now: time.Now}
}

func (p *Presence) Touch(ctx context.Context, userID, connID string) error {
	key := presencePrefix + userID
	now := p.now()
	_, err := p.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.Add(presenceTTL).UnixMilli()), Member: connID})
		pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.UnixMilli(), 10))
		pipe.Expire(ctx, key, 2*presenceTTL)
		return nil
	})
	return err
}

func (p *Presence) Leave(ctx context.Context, userID, connID string) error {
	return p.rdb.ZRem(ctx, presencePrefix+userID, connID).Err()
}

func (p *Presence) Online(ctx context.Context, userIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	min := strconv.FormatInt(p.now().UnixMilli(), 10)
	cmds := make([]*redis.IntCmd, len(userIDs))
	_, err := p.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for i, id := range userIDs {
			cmds[i] = pipe.ZCount(ctx, presencePrefix+id, min, "+inf")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i, id := range userIDs {
		result[id] = cmds[i].Val() > 0
	}
	return result, nil
}

func (p *Presence) IsOnline(ctx context.Context, userID string) (bool, error) {
	m, err := p.Online(ctx, []string{userID})
	if err != nil {
		return false, err
	}
	return m[userID], nil
}
