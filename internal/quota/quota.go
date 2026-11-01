package quota

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrLimit = errors.New("rate or concurrency limit exceeded")

type Limiter struct{ Client *redis.Client }

func New(raw string) (*Limiter, error) {
	o, e := redis.ParseURL(raw)
	if e != nil {
		return nil, e
	}
	return &Limiter{redis.NewClient(o)}, nil
}

var acquire = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',ARGV[2])
local used=tonumber(redis.call('GET',KEYS[1]) or '0')
if used>=tonumber(ARGV[4]) or redis.call('ZCARD',KEYS[2])>=tonumber(ARGV[5]) then return 0 end
local n=redis.call('INCR',KEYS[1])
if n==1 then redis.call('PEXPIRE',KEYS[1],60000) end
redis.call('ZADD',KEYS[2],ARGV[3],ARGV[1])
redis.call('PEXPIRE',KEYS[2],ARGV[6])
return 1
`)

func (l *Limiter) Acquire(ctx context.Context, key, id string, rpm, concurrency int, ttl time.Duration) (func(), error) {
	now, e := l.Client.Time(ctx).Result()
	if e != nil {
		return nil, e
	}
	keys := []string{"quota:{" + key + "}:rpm", "quota:{" + key + "}:active"}
	v, e := acquire.Run(ctx, l.Client, keys, id, strconv.FormatInt(now.UnixMilli(), 10), strconv.FormatInt(now.Add(ttl).UnixMilli(), 10), rpm, concurrency, ttl.Milliseconds()).Int()
	if e != nil {
		return nil, e
	}
	if v != 1 {
		return nil, ErrLimit
	}
	return func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = l.Client.ZRem(c, keys[1], id).Err()
	}, nil
}
