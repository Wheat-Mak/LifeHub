package voucher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	DetailKeyPrefix = "cache:voucher:"
	DetailTTL       = 30 * time.Minute
	// 显式排除库存，防止把缓存快照当作剩余库存。
	detailColumns = "id, shop_id, title, sub_title, rules, pay_value, actual_value, status, begin_time, end_time, create_time, update_time"
)

// FindByID 只处理浏览详情的回源；下单 Lua 直接读取同一 Key，缺失时不查 MySQL。
func (s *Service) FindByID(ctx context.Context, voucherID uint64) (*Detail, error) {
	if voucherID == 0 {
		return nil, ErrInvalidID
	}
	key := DetailKeyPrefix + strconv.FormatUint(voucherID, 10)
	data, err := s.redis.Get(ctx, key).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var detail Detail
	if err == nil {
		if err := json.Unmarshal(data, &detail); err != nil {
			return nil, fmt.Errorf("解析消费券详情缓存: %w", err)
		}
		if detail.ID != voucherID || detail.BeginTime.IsZero() || detail.EndTime.IsZero() {
			return nil, errors.New("消费券详情缓存字段无效")
		}
		if detail.Status != 1 {
			return nil, ErrUnavailable
		}
		return &detail, nil
	}
	// 未命中才查询详情；不存在或数据库异常均不写入正值缓存。
	err = s.db.WithContext(ctx).Select(detailColumns).
		Where("id = ? AND status = 1", voucherID).Take(&detail).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, err
	}
	data, err = encodeDetail(detail)
	if err != nil {
		return nil, err
	}
	if err := s.redis.Set(ctx, key, data, DetailTTL).Err(); err != nil {
		return nil, err
	}
	return &detail, nil
}
