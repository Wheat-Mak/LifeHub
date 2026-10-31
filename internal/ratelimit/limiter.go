// Package ratelimit 通过共享 Redis 对秒杀 HTTP 入口实施全局与用户两级令牌桶。
package ratelimit

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"lifemate/internal/redisstore"
)

const (
	maxValue  = 1e9
	maxMillis = int64(math.MaxInt64 / int64(time.Millisecond))
)

type Bucket struct {
	Rate  float64 // 每秒补充令牌数，允许小数。
	Burst int64   // 最大令牌数。
}

type Config struct {
	Prefix string
	Global Bucket
	User   Bucket
}

func DefaultConfig() Config {
	return Config{Prefix: "rate:limit:seckill", Global: Bucket{Rate: 100, Burst: 100}, User: Bucket{Rate: 1, Burst: 2}}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Prefix) == "" {
		return errors.New("限流 Key 前缀不能为空")
	}
	for _, rule := range []struct {
		name   string
		bucket Bucket
	}{{"全局", c.Global}, {"用户", c.User}} {
		b := rule.bucket
		if math.IsNaN(b.Rate) || math.IsInf(b.Rate, 0) || b.Rate <= 0 || b.Rate > maxValue || b.Burst < 1 || b.Burst > int64(maxValue) {
			return fmt.Errorf("%s限流速率须大于0且不超过1e9，容量须为1到1e9的整数", rule.name)
		}
		if refillMillis(b) > float64(maxMillis) {
			return fmt.Errorf("%s限流满桶恢复时间超出范围", rule.name)
		}
	}
	return nil
}

func refillMillis(b Bucket) float64 { return math.Max(1, math.Ceil(float64(b.Burst)*1000/b.Rate)) }

type Scope string

const (
	Global Scope = "global"
	User   Scope = "user"
)

type Decision struct {
	Allowed    bool
	LimitedBy  Scope
	RetryAfter time.Duration // 当前状态下预计至少等待的时间，不是额度预留。
}

//go:embed token_bucket.lua
var source string
var script = redis.NewScript(source)

type Limiter struct {
	client *redis.Client
	cfg    Config
}

func New(client *redis.Client, cfg Config) (*Limiter, error) {
	if client == nil {
		return nil, errors.New("限流 Redis 客户端不能为空")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.Prefix = strings.TrimSpace(cfg.Prefix)
	return &Limiter{client: client, cfg: cfg}, nil
}

// Allow 一次原子判断两个桶，仅全部满足时各消耗一个令牌。
func (l *Limiter) Allow(ctx context.Context, userID uint64) (Decision, error) {
	if userID == 0 {
		return Decision{}, errors.New("限流用户ID必须为正整数")
	}
	keys := []string{l.cfg.Prefix + ":global", l.cfg.Prefix + ":user:" + strconv.FormatUint(userID, 10)}
	values, err := script.Run(ctx, redisstore.NoRetryScripts(l.client), keys,
		l.cfg.Global.Rate, l.cfg.Global.Burst, int64(refillMillis(l.cfg.Global)),
		l.cfg.User.Rate, l.cfg.User.Burst, int64(refillMillis(l.cfg.User)), maxMillis).Int64Slice()
	if err != nil {
		return Decision{}, err
	}
	if len(values) != 3 {
		return Decision{}, errors.New("限流Lua返回格式错误")
	}
	if values[0] == 1 {
		return Decision{Allowed: true}, nil
	}
	if values[0] != 0 || values[2] < 1 || values[2] > maxMillis {
		return Decision{}, errors.New("限流Lua返回值错误")
	}
	var scope Scope
	switch values[1] {
	case 1:
		scope = Global
	case 2:
		scope = User
	default:
		return Decision{}, errors.New("限流Lua返回维度错误")
	}
	return Decision{LimitedBy: scope, RetryAfter: time.Duration(values[2]) * time.Millisecond}, nil
}
