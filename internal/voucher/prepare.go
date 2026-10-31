package voucher

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"lifemate/internal/redisstore"
)

type cachedDetail struct {
	Detail
	BeginUnix int64 `json:"beginUnix"`
	EndUnix   int64 `json:"endUnix"`
}

func encodeDetail(detail Detail) ([]byte, error) {
	return json.Marshal(cachedDetail{Detail: detail, BeginUnix: detail.BeginTime.Unix(), EndUnix: detail.EndTime.Unix()})
}

var prepareScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 1 or redis.call('EXISTS', KEYS[3]) == 1 then return 1 end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('SET', KEYS[2], ARGV[2])
for i = 3, #ARGV do redis.call('SADD', KEYS[3], ARGV[i]) end
return 0
`)

// Prepare 只在停止该活动流量且无待消费消息时手动执行，禁止覆盖已有秒杀状态。
func (s *Service) Prepare(ctx context.Context, voucherID uint64) error {
	if voucherID == 0 {
		return ErrInvalidID
	}
	var record Voucher
	var buyers []uint64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Take(&record, voucherID).Error; err != nil {
			return err
		}
		return tx.Model(&Order{}).Where("voucher_id = ?", voucherID).Pluck("user_id", &buyers).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrUnavailable
	}
	if err != nil {
		return err
	}
	if record.Stock < 0 || !record.EndTime.After(record.BeginTime) {
		return errors.New("券库存或活动时间无效")
	}
	data, err := encodeDetail(record.Detail)
	if err != nil {
		return err
	}
	args := []interface{}{data, record.Stock}
	for _, buyer := range buyers {
		args = append(args, strconv.FormatUint(buyer, 10))
	}
	code, err := prepareScript.Run(ctx, redisstore.NoRetryScripts(s.redis), voucherKeys(voucherID), args...).Int()
	if err != nil {
		return err
	}
	if code != 0 {
		return ErrAlreadyPrepared
	}
	return nil
}
