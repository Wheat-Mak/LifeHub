package voucher

import (
	"context"
	"errors"
	"strconv"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

const (
	OrderPending uint8 = 1
	OrderPaid    uint8 = 2
	OrderClosed  uint8 = 4
)

var (
	ErrInvalidSnapshot = errors.New("订单事实不完整或无效")
	ErrOrderNotFound   = errors.New("订单尚未落库或不存在")
	ErrOrderForbidden  = errors.New("无权操作该订单")
	ErrNotPayable      = errors.New("订单已关闭、已过期或信息冲突，无法支付")
	ErrNotDue          = errors.New("订单尚未到关闭时间")
)

func (m OrderMessage) Validate() error {
	if m.ID <= 0 || m.UserID == 0 || m.VoucherID == 0 {
		return ErrInvalidID
	}
	if m.Amount > 9007199254740991 || m.ReservedAt <= 0 || m.ExpireAt <= m.ReservedAt ||
		m.ExpireAt-m.ReservedAt > (24*time.Hour).Milliseconds() {
		return ErrInvalidSnapshot
	}
	return nil
}

func (m OrderMessage) order(status uint8) Order {
	return Order{ID: m.ID, UserID: m.UserID, VoucherID: m.VoucherID, Amount: m.Amount,
		ReservedAt: time.UnixMilli(m.ReservedAt).UTC(), ExpireAt: time.UnixMilli(m.ExpireAt).UTC(), PayType: 1, Status: status}
}

func (o Order) message() OrderMessage {
	return OrderMessage{ID: o.ID, UserID: o.UserID, VoucherID: o.VoucherID, Amount: o.Amount,
		ReservedAt: o.ReservedAt.UnixMilli(), ExpireAt: o.ExpireAt.UnixMilli()}
}

func parsePreorder(id int64, fields map[string]string) (OrderMessage, error) {
	m := OrderMessage{ID: id}
	var err error
	for _, field := range []struct {
		key    string
		target *uint64
	}{
		{"userId", &m.UserID}, {"voucherId", &m.VoucherID}, {"amount", &m.Amount},
	} {
		*field.target, err = strconv.ParseUint(fields[field.key], 10, 64)
		if err != nil {
			return m, ErrInvalidSnapshot
		}
	}
	for _, field := range []struct {
		key    string
		target *int64
	}{
		{"reservedAt", &m.ReservedAt}, {"expireAt", &m.ExpireAt},
	} {
		*field.target, err = strconv.ParseInt(fields[field.key], 10, 64)
		if err != nil {
			return m, ErrInvalidSnapshot
		}
	}
	return m, m.Validate()
}

func (s *Service) FindOrder(ctx context.Context, userID uint64, orderID int64) (*Order, error) {
	if userID == 0 || orderID <= 0 {
		return nil, ErrInvalidID
	}
	var order Order
	err := s.db.WithContext(ctx).Where("id = ?", orderID).Take(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, ErrOrderForbidden
	}
	return &order, nil
}

// Pay 模拟支付：Hash 命中无前置 SELECT，Hash 缺失/读取失败才回查 MySQL。
func (s *Service) Pay(ctx context.Context, userID uint64, orderID int64) error {
	if userID == 0 || orderID <= 0 {
		return ErrInvalidID
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	fields, readErr := s.redis.HGetAll(readCtx, OrderKeyPrefix+strconv.FormatInt(orderID, 10)).Result()
	cancel()
	var message OrderMessage
	var err error
	if readErr != nil || len(fields) == 0 {
		order, err := s.FindOrder(ctx, userID, orderID)
		if err != nil {
			return err
		}
		message = order.message()
	} else {
		message, err = parsePreorder(orderID, fields)
		if err != nil {
			return err
		}
	}
	if message.UserID != userID {
		return ErrOrderForbidden
	}
	if err := message.Validate(); err != nil {
		return err
	}
	return s.orderTransaction(ctx, func(tx *gorm.DB) error {
		// 以无业务变更的写操作锁住行；下一条语句才读取数据库当前时间。
		// 避免在锁等待前计算 NOW，导致等待跨过截止时间后仍可支付。
		locked := tx.Exec("UPDATE tb_voucher_order SET id = id WHERE id = ?", message.ID)
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected == 0 {
			order := message.order(OrderPending)
			if err := tx.Create(&order).Error; err != nil {
				if !duplicateOrder(err) {
					return err
				}
			} else if err := deductStock(tx, message.VoucherID); err != nil {
				return err
			}
		}
		paid, err := payMatchingOrder(tx, message)
		if err != nil {
			return err
		}
		if !paid {
			return ErrNotPayable
		} // 包含补建期间跨越截止时间，整个事务回滚。
		return nil
	})
}

func payMatchingOrder(tx *gorm.DB, m OrderMessage) (bool, error) {
	// clientFoundRows=true 保证已支付的无变更匹配也返回 1；重复支付不改变 pay_time。
	result := tx.Exec(`UPDATE tb_voucher_order SET
		pay_time = IF(status = 1, UTC_TIMESTAMP(3), pay_time), status = 2
		WHERE id = ? AND user_id = ? AND voucher_id = ? AND amount = ? AND reserved_at = ? AND expire_at = ?
		AND (status = 2 OR (status = 1 AND expire_at > UTC_TIMESTAMP(3)))`,
		m.ID, m.UserID, m.VoucherID, m.Amount, time.UnixMilli(m.ReservedAt).UTC(), time.UnixMilli(m.ExpireAt).UTC())
	return result.RowsAffected == 1, result.Error
}

func duplicateOrder(err error) bool {
	var conflict *driver.MySQLError
	return errors.As(err, &conflict) && conflict.Number == 1062
}

// 仅重试 MySQL 明确的死锁/锁等待失败，其他错误（包括提交结果不确定）交给调用方重试。
func (s *Service) orderTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = s.db.WithContext(ctx).Transaction(fn)
		var failure *driver.MySQLError
		if !errors.As(err, &failure) || (failure.Number != 1213 && failure.Number != 1205) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
