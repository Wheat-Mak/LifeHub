package voucher

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"lifemate/internal/redisstore"
)

var (
	ErrUnavailable     = errors.New("秒杀券不存在或未上架")
	ErrNotStarted      = errors.New("秒杀尚未开始")
	ErrEnded           = errors.New("秒杀已经结束")
	ErrOutOfStock      = errors.New("库存不足")
	ErrDuplicateOrder  = errors.New("不能重复下单")
	ErrInvalidID       = errors.New("用户和消费券 ID 必须是正整数")
	ErrNotPrepared     = errors.New("秒杀活动尚未准备")
	ErrAlreadyPrepared = errors.New("秒杀库存或购买集合已存在，禁止重复初始化")
	ErrOrderConflict   = errors.New("订单消息与已有订单冲突")
)

const (
	StockKeyPrefix    = "seckill:stock:"
	BuyersKeyPrefix   = "seckill:buyers:"
	OrderKeyPrefix    = "seckill:order:"
	RestoredKeyPrefix = "seckill:restored:"
)

//go:embed seckill.lua
var seckillSource string
var seckillScript = redis.NewScript(seckillSource)

type IDGenerator interface {
	Next(context.Context) (int64, error)
}

type OrderMessage struct {
	ID         int64  `json:"id,string"`
	UserID     uint64 `json:"userId"`
	VoucherID  uint64 `json:"voucherId"`
	Amount     uint64 `json:"amount"`
	ReservedAt int64  `json:"reservedAt"` // Redis 成功预扣时的 Unix 毫秒。
	ExpireAt   int64  `json:"expireAt"`
}

type Publisher interface {
	Publish(context.Context, OrderMessage) error
}

type Service struct {
	db          *gorm.DB
	redis       *redis.Client
	ids         IDGenerator
	publisher   Publisher
	orderConfig OrderConfig
}

func NewService(db *gorm.DB, client *redis.Client, ids IDGenerator, publisher Publisher) *Service {
	return &Service{db: db, redis: client, ids: ids, publisher: publisher, orderConfig: DefaultOrderConfig()}
}

type OrderConfig struct {
	PaymentTimeout time.Duration
	PreorderGrace  time.Duration // 截止时间后继续保留 Hash 的时间。
}

func DefaultOrderConfig() OrderConfig {
	return OrderConfig{PaymentTimeout: time.Minute, PreorderGrace: 24 * time.Hour}
}

func (c OrderConfig) Validate() error {
	if c.PaymentTimeout < time.Millisecond || c.PaymentTimeout > 24*time.Hour ||
		c.PreorderGrace < time.Millisecond || c.PreorderGrace > 30*24*time.Hour {
		return errors.New("支付期限须在 1ms～24h，预订单宽限保留期须在 1ms～720h")
	}
	return nil
}

func NewServiceWithOrderConfig(db *gorm.DB, client *redis.Client, ids IDGenerator, publisher Publisher, cfg OrderConfig) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := NewService(db, client, ids, publisher)
	s.orderConfig = cfg
	return s, nil
}

func (s *Service) ListOfShop(ctx context.Context, shopID uint64) ([]Detail, error) {
	records := make([]Detail, 0)
	err := s.db.WithContext(ctx).Select(detailColumns).
		Where("shop_id = ? AND status = 1", shopID).Order("id").Find(&records).Error
	return records, err
}

func voucherKeys(id uint64) []string {
	suffix := strconv.FormatUint(id, 10)
	return []string{DetailKeyPrefix + suffix, StockKeyPrefix + suffix, BuyersKeyPrefix + suffix}
}

// Seckill 请求侧只访问 Redis 和 MQ；成功仅表示消息投递成功，不表示订单已落库。
func (s *Service) Seckill(ctx context.Context, userID, voucherID uint64) (OrderMessage, error) {
	var empty OrderMessage
	if userID == 0 || voucherID == 0 {
		return empty, ErrInvalidID
	}
	if s.publisher == nil {
		return empty, errors.New("未配置订单消息生产者")
	}
	orderID, err := s.ids.Next(ctx)
	if err != nil {
		return empty, err
	}
	if orderID <= 0 {
		return empty, ErrInvalidID
	}
	keys := append(voucherKeys(voucherID), OrderKeyPrefix+strconv.FormatInt(orderID, 10))
	values, err := seckillScript.Run(ctx, redisstore.NoRetryScripts(s.redis), keys,
		strconv.FormatUint(userID, 10), strconv.FormatInt(orderID, 10),
		s.orderConfig.PaymentTimeout.Milliseconds(), s.orderConfig.PreorderGrace.Milliseconds(),
		strconv.FormatUint(voucherID, 10)).Slice()
	if err != nil {
		return empty, err
	}
	if len(values) == 0 {
		return empty, errors.New("秒杀 Lua 返回空结果")
	}
	code, err := strconv.Atoi(fmt.Sprint(values[0]))
	if err != nil {
		return empty, err
	}
	switch code {
	case 0:
	case 1:
		return empty, ErrOutOfStock
	case 2:
		return empty, ErrDuplicateOrder
	case 3:
		return empty, ErrNotPrepared
	case 4:
		return empty, ErrUnavailable
	case 5:
		return empty, ErrNotStarted
	case 6:
		return empty, ErrEnded
	default:
		return empty, fmt.Errorf("秒杀 Lua 返回未知状态：%d", code)
	}
	if len(values) != 4 {
		return empty, errors.New("秒杀 Lua 返回的订单事实不完整")
	}
	message, err := parsePreorder(orderID, map[string]string{
		"userId": strconv.FormatUint(userID, 10), "voucherId": strconv.FormatUint(voucherID, 10),
		"amount": fmt.Sprint(values[1]), "reservedAt": fmt.Sprint(values[2]), "expireAt": fmt.Sprint(values[3]),
	})
	if err != nil {
		return empty, err
	}
	if err := s.publisher.Publish(ctx, message); err != nil {
		// 普通消息发送的失败/超时可能发生于 Broker 已接收之后，不能擅自回补 Redis。
		return empty, fmt.Errorf("Redis 已预扣，但 MQ 投递结果失败或不确定，orderId=%d: %w", orderID, err)
	}
	return message, nil
}
