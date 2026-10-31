package shop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

var (
	ErrInvalidUpdate = errors.New("店铺更新参数无效")
	ErrNotFound      = errors.New("店铺不存在")
)

// InvalidationError 表示 MySQL 已提交；返回错误不代表数据库回滚。
type InvalidationError struct{ Err error }

func (e *InvalidationError) Error() string {
	return "MySQL 已提交，但店铺缓存失效失败: " + e.Err.Error()
}
func (e *InvalidationError) Unwrap() error { return e.Err }

// UpdateInput 使用指针区分未传字段和明确的零值，禁止修改 ID 和数据库时间戳。
type UpdateInput struct {
	ID        uint64   `json:"id"`
	Name      *string  `json:"name"`
	TypeID    *uint64  `json:"typeId"`
	Images    *string  `json:"images"`
	Area      *string  `json:"area"`
	Address   *string  `json:"address"`
	X         *float64 `json:"x"`
	Y         *float64 `json:"y"`
	AvgPrice  *uint64  `json:"avgPrice"`
	Sold      *uint32  `json:"sold"`
	Comments  *uint32  `json:"comments"`
	Score     *uint32  `json:"score"`
	OpenHours *string  `json:"openHours"`
}

func (input UpdateInput) columns() (map[string]any, error) {
	if input.ID == 0 {
		return nil, ErrInvalidUpdate
	}
	columns := make(map[string]any)
	for _, field := range []struct {
		name     string
		value    *string
		max      int
		required bool
	}{
		{"name", input.Name, 128, true}, {"images", input.Images, 1024, false},
		{"area", input.Area, 128, false}, {"address", input.Address, 255, true}, {"open_hours", input.OpenHours, 32, false},
	} {
		if field.value == nil {
			continue
		}
		if utf8.RuneCountInString(*field.value) > field.max || (field.required && strings.TrimSpace(*field.value) == "") {
			return nil, fmt.Errorf("%w: %s", ErrInvalidUpdate, field.name)
		}
		columns[field.name] = *field.value
	}
	if input.TypeID != nil {
		if *input.TypeID == 0 {
			return nil, ErrInvalidUpdate
		}
		columns["type_id"] = *input.TypeID
	}
	if input.X != nil {
		if *input.X < 0 {
			return nil, ErrInvalidUpdate
		}
		columns["x"] = *input.X
	}
	if input.Y != nil {
		if *input.Y < 0 {
			return nil, ErrInvalidUpdate
		}
		columns["y"] = *input.Y
	}
	if input.AvgPrice != nil {
		columns["avg_price"] = *input.AvgPrice
	}
	if input.Sold != nil {
		columns["sold"] = *input.Sold
	}
	if input.Comments != nil {
		columns["comments"] = *input.Comments
	}
	if input.Score != nil {
		columns["score"] = *input.Score
	}
	if len(columns) == 0 {
		return nil, ErrInvalidUpdate
	}
	return columns, nil
}

func (s *Service) Update(ctx context.Context, input UpdateInput) error {
	columns, err := input.columns()
	if err != nil {
		return err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Shop{}).Where("id = ?", input.ID).Updates(columns)
		if result.Error != nil {
			return result.Error
		}
		// MySQL 相同值更新可能返回 0 行，不能直接当作店铺不存在。
		if result.RowsAffected == 0 {
			var record Shop
			if err := tx.Select("id").Take(&record, input.ID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			} else {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Transaction 返回 nil 后 MySQL 已提交；更新路径不等待重建锁。
	if err := s.Invalidate(ctx, input.ID); err != nil {
		return &InvalidationError{Err: err}
	}
	return nil
}
