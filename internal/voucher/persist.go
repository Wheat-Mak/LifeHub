package voucher

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// PersistOrder 仅由消费者落库。订单号主键用于消息幂等，插订单与条件扣库存同一事务提交。
func (s *Service) PersistOrder(ctx context.Context, message OrderMessage) error {
	if err := message.Validate(); err != nil {
		return err
	}
	err := s.orderTransaction(ctx, func(tx *gorm.DB) error {
		order := message.order(OrderPending)
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		return deductStock(tx, message.VoucherID)
	})
	if !duplicateOrder(err) {
		return err
	}
	// 订单号主键冲突的事务已回滚。仅确认完全相同的订单为已成功消费。
	var existing Order
	if lookup := s.db.WithContext(ctx).Where("id = ?", message.ID).Take(&existing).Error; lookup != nil {
		if errors.Is(lookup, gorm.ErrRecordNotFound) {
			return ErrOrderConflict
		}
		return lookup
	}
	if existing.message() != message {
		return ErrOrderConflict
	}
	return nil
}

func deductStock(tx *gorm.DB, voucherID uint64) error {
	updated := tx.Model(&Voucher{}).Where("id = ? AND stock > 0", voucherID).
		Update("stock", gorm.Expr("stock - 1"))
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrOutOfStock
	}
	return nil
}
