package main

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v9"
)

type RateLimiter struct {
	redis  *redis.Client
	burst  int
	refill int
}

func NewRateLimiter(rdb *redis.Client, burst, refill int) *RateLimiter {
	return &RateLimiter{redis: rdb, burst: burst, refill: refill}
}

var tokenBucketScript = redis.NewScript(`
local key = KEYS[1]
local burst = tonumber(ARGV[1])
local refill = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local bucket = redis.call("HMGET", key, "tokens", "ts")
local tokens = tonumber(bucket[1]) or burst
local ts = tonumber(bucket[2]) or now
local delta = math.max(0, now - ts)
tokens = math.min(burst, tokens + (delta * refill))
if tokens < 1 then
  redis.call("HMSET", key, "tokens", tokens, "ts", now)
  redis.call("EXPIRE", key, ttl)
  return 0
end
tokens = tokens - 1
redis.call("HMSET", key, "tokens", tokens, "ts", now)
redis.call("EXPIRE", key, ttl)
return 1
`)

func (r *RateLimiter) Allow(ctx context.Context, ip string) (bool, error) {
	res, err := tokenBucketScript.Run(ctx, r.redis, []string{fmt.Sprintf("ratelimit:%s", ip)}, r.burst, r.refill, float64(redisTime()), 60).Int()
	return res == 1, err
}

func redisTime() int64 {
	return timeNowUnix()
}
