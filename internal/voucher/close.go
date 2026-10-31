package voucher

import (
	"context"
	_ "embed"
	"errors"
	"strconv"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lifemate/internal/redisstore"
)

//go:embed restore.lua
var restoreSource string
var restoreScript = redis.NewScript(restoreSource)

// CloseExpiredOrder 先提交 MySQL，再幂等回补 Redis；后半段失败时必须由 MQ 重试。
func (s *Service) CloseExpiredOrder(ctx context.Context, message OrderMessage) error {
	if err := message.Validate(); err != nil {
		return err
	}
	order := message.order(OrderClosed)
	err := s.orderTransaction(ctx, func(tx *gorm.DB) error {
		// 未落库时直接写入关闭记录。没有已提交的数据库扣库，不做先扣再补。
		result := tx.Exec(`INSERT INTO tb_voucher_order
			(id,user_id,voucher_id,amount,reserved_at,expire_at,pay_type,status,create_time,update_time)
			SELECT ?,?,?,?,?,?,1,4,UTC_TIMESTAMP(3),UTC_TIMESTAMP(3)
			WHERE ? <= UTC_TIMESTAMP(3)`, order.ID, order.UserID, order.VoucherID, order.Amount,
			order.ReservedAt, order.ExpireAt, order.ExpireAt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotDue
		}
		return nil
	})
	closed := err == nil
	if duplicateOrder(err) {
		// 插入冲突的事务已回滚，再读取已提交记录，避免沿用旧快照。
		err = s.orderTransaction(ctx, func(tx *gorm.DB) error {
			closed = false
			var existing Order
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", message.ID).Take(&existing).Error; err != nil {
				return err
			}
			if existing.message() != message {
				return ErrOrderConflict
			}
			switch existing.Status {
			case OrderPaid:
				return nil
			case OrderClosed:
				closed = true
				return nil
			case OrderPending:
				result := tx.Model(&Order{}).Where("id = ? AND status = ? AND expire_at <= UTC_TIMESTAMP(3)", message.ID, OrderPending).
					Update("status", OrderClosed)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrNotDue
				}
				stock := tx.Model(&Voucher{}).Where("id = ? AND stock < 2147483647", message.VoucherID).
					Update("stock", gorm.Expr("stock + 1"))
				if stock.Error != nil {
					return stock.Error
				}
				if stock.RowsAffected != 1 {
					return errors.New("关闭订单的数据库库存不存在或溢出")
				}
				closed = true
				return nil
			default:
				return ErrOrderConflict
			}
		})
	}
	if err != nil {
		return err
	}
	if !closed {
		return nil
	}
	suffix := strconv.FormatUint(message.VoucherID, 10)
	return restoreScript.Run(ctx, redisstore.NoRetryScripts(s.redis),
		[]string{StockKeyPrefix + suffix, RestoredKeyPrefix + suffix}, strconv.FormatInt(message.ID, 10)).Err()
}
