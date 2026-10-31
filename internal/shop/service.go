package shop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	CacheKeyPrefix  = "cache:shop:"
	LockKeyPrefix   = "lock:shop:"
	CacheTTL        = 30 * time.Minute
	NullTTL         = 2 * time.Minute
	lockTTL         = 10 * time.Second
	loadTimeout     = 5 * time.Second
	maxLoadAttempts = 3
)

// 缓存正值不设置物理 TTL；空字符串表示不存在，使用短物理 TTL。
type cacheEntry struct {
	Data       *Shop     `json:"data"`
	ExpireTime time.Time `json:"expireTime"`
}

type Service struct {
	db           *gorm.DB
	redis        *redis.Client
	refreshSlots chan struct{}
}

func NewService(db *gorm.DB, client *redis.Client) *Service {
	return &Service{db: db, redis: client, refreshSlots: make(chan struct{}, 10)}
}

func (s *Service) FindByID(ctx context.Context, id uint64) (*Shop, error) {
	entry, hit, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	if hit {
		if entry.Data != nil && !entry.ExpireTime.After(time.Now()) {
			s.refreshAsync(id)
		}
		return entry.Data, nil
	}
	// 冷缓存等待持锁请求回填，等待有上限；不让所有请求同时回源。
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	attempts := 0
	for {
		entry, hit, err := s.read(ctx, id)
		if err != nil {
			return nil, err
		}
		if hit {
			return entry.Data, nil
		}
		if attempts >= maxLoadAttempts {
			return nil, ErrGenerationChanged
		}
		owner, acquired, err := s.lock(ctx, id)
		if err != nil {
			return nil, err
		}
		if acquired {
			value, err := s.loadOnMiss(ctx, id, owner)
			if !errors.Is(err, ErrGenerationChanged) {
				return value, err
			}
			// loadOnMiss 已释放锁；丢弃失效结果，先读当前缓存，再有限次回源。
			attempts++
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) loadOnMiss(ctx context.Context, id uint64, owner string) (*Shop, error) {
	defer s.unlock(id, owner)
	// 双重检查：前一持锁者可能刚完成回填并释放了锁。
	entry, hit, err := s.read(ctx, id)
	if err != nil {
		return nil, err
	}
	if hit {
		return entry.Data, nil
	}
	return s.load(ctx, id)
}

func (s *Service) read(ctx context.Context, id uint64) (cacheEntry, bool, error) {
	value, err := s.redis.Get(ctx, CacheKeyPrefix+strconv.FormatUint(id, 10)).Result()
	if errors.Is(err, redis.Nil) {
		return cacheEntry{}, false, nil
	}
	if err != nil {
		return cacheEntry{}, false, err
	}
	if value == "" {
		return cacheEntry{}, true, nil
	}
	var entry cacheEntry
	if err := json.Unmarshal([]byte(value), &entry); err != nil {
		return cacheEntry{}, false, fmt.Errorf("解析商铺缓存: %w", err)
	}
	if entry.Data == nil || entry.Data.ID != id || entry.ExpireTime.IsZero() {
		return cacheEntry{}, false, errors.New("商铺缓存结构无效")
	}
	return entry, true, nil
}

func (s *Service) load(ctx context.Context, id uint64) (*Shop, error) {
	// 必须先取得代次，再从主库读取；不得在 SQL 返回后才捕获代次。
	generation, err := s.generation(ctx, id)
	if err != nil {
		return nil, err
	}
	var record Shop
	err = s.db.WithContext(ctx).Take(&record, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, s.fill(ctx, id, generation, nil, NullTTL)
	}
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(cacheEntry{Data: &record, ExpireTime: time.Now().UTC().Add(CacheTTL)})
	if err != nil {
		return nil, err
	}
	if err := s.fill(ctx, id, generation, data, 0); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Service) refreshAsync(id uint64) {
	select {
	case s.refreshSlots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-s.refreshSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		owner, acquired, err := s.lock(ctx, id)
		if err != nil {
			slog.Error("商铺缓存重建加锁失败", "shopId", id, "error", err)
			return
		}
		if !acquired {
			return
		}
		defer s.unlock(id, owner)
		entry, hit, err := s.read(ctx, id)
		if err == nil && hit && (entry.Data == nil || entry.ExpireTime.After(time.Now())) {
			return
		}
		if err == nil {
			_, err = s.load(ctx, id)
		}
		if err != nil && !errors.Is(err, ErrGenerationChanged) {
			slog.Error("商铺缓存重建失败", "shopId", id, "error", err)
		}
	}()
}

func (s *Service) lock(ctx context.Context, id uint64) (string, bool, error) {
	owner, err := randomTag()
	if err != nil {
		return "", false, err
	}
	acquired, err := s.redis.SetNX(ctx, LockKeyPrefix+strconv.FormatUint(id, 10), owner, lockTTL).Result()
	return owner, acquired, err
}

// 只释放自己持有的锁，防止锁过期后误删其他请求新获得的锁。
var unlockScript = redis.NewScript(`
if redis.call('get', KEYS[1]) == ARGV[1] then
    return redis.call('del', KEYS[1])
end
return 0`)

func (s *Service) unlock(id uint64, owner string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := unlockScript.Run(ctx, s.redis, []string{LockKeyPrefix + strconv.FormatUint(id, 10)}, owner).Err(); err != nil {
		slog.Error("商铺缓存锁释放失败", "shopId", id, "error", err)
	}
}

// 列表沿用参考实现直接查 MySQL，不创建分类缓存或 GEO 索引。
func (s *Service) List(ctx context.Context, name string, typeID uint64, current, size int) ([]Shop, error) {
	query := s.db.WithContext(ctx).Model(&Shop{})
	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}
	if typeID != 0 {
		query = query.Where("type_id = ?", typeID)
	}
	records := make([]Shop, 0)
	err := query.Order("id").Offset((current - 1) * size).Limit(size).Find(&records).Error
	return records, err
}
