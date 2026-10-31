package shop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"lifemate/internal/redisstore"
)

const GenerationKeyPrefix = "cache:shop:gen:"

var ErrGenerationChanged = errors.New("店铺在回源期间发生更新，请稍后重试")

// 初始化和读取在同一脚本内执行，不覆盖其他重建或更新路径已写入的代次。
var generationScript = redis.NewScript(`
local generation = redis.call('get', KEYS[1])
if generation then
    return generation
end
redis.call('set', KEYS[1], ARGV[1])
return ARGV[1]`)

// 代次缺失同样拒绝回填。不得在这里初始化代次，否则旧任务可能重新获得写入资格。
var fillScript = redis.NewScript(`
if redis.call('get', KEYS[1]) ~= ARGV[1] then
    return 0
end
if ARGV[3] == '0' then
    redis.call('set', KEYS[2], ARGV[2])
else
    redis.call('set', KEYS[2], ARGV[2], 'PX', ARGV[3])
end
return 1`)

// 不获取、不删除重建锁；代次切换和数据缓存删除不可被旧回填穿插。
var invalidateScript = redis.NewScript(`
redis.call('set', KEYS[1], ARGV[1])
redis.call('del', KEYS[2])
return 1`)

func randomTag() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (s *Service) generation(ctx context.Context, id uint64) (string, error) {
	candidate, err := randomTag()
	if err != nil {
		return "", err
	}
	return generationScript.Run(ctx, redisstore.NoRetryScripts(s.redis), []string{GenerationKeyPrefix + strconv.FormatUint(id, 10)}, candidate).Text()
}

func (s *Service) fill(ctx context.Context, id uint64, generation string, value []byte, ttl time.Duration) error {
	suffix := strconv.FormatUint(id, 10)
	accepted, err := fillScript.Run(ctx, s.redis,
		[]string{GenerationKeyPrefix + suffix, CacheKeyPrefix + suffix}, generation, string(value), ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if accepted != 1 {
		return ErrGenerationChanged
	}
	return nil
}

// Invalidate 在数据库提交后调用。既删除正值也删除空值，保留独立代次且不设置 TTL。
// 手动 SQL 或未来新增的其他写入口也必须显式调用同一协议。
func (s *Service) Invalidate(ctx context.Context, id uint64) error {
	generation, err := randomTag()
	if err != nil {
		return err
	}
	suffix := strconv.FormatUint(id, 10)
	return invalidateScript.Run(ctx, redisstore.NoRetryScripts(s.redis),
		[]string{GenerationKeyPrefix + suffix, CacheKeyPrefix + suffix}, generation).Err()
}
