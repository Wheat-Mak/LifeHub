package voucher

import "time"

// Detail 是缓存和接口使用的券详情，不包含实时库存。
type Detail struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ShopID      *uint64   `json:"shopId,omitempty"`
	Title       string    `json:"title"`
	SubTitle    *string   `json:"subTitle,omitempty"`
	Rules       *string   `json:"rules,omitempty"`
	PayValue    uint64    `json:"payValue"`
	ActualValue int64     `json:"actualValue"`
	Status      uint8     `gorm:"default:1" json:"status"`
	BeginTime   time.Time `json:"beginTime"`
	EndTime     time.Time `json:"endTime"`
	CreateTime  time.Time `gorm:"autoCreateTime" json:"createTime"`
	UpdateTime  time.Time `gorm:"autoUpdateTime" json:"updateTime"`
}

func (Detail) TableName() string { return "tb_voucher" }

// 一个活动只有一种券，活动时间和数据库库存与券信息共用一行。
type Voucher struct {
	Detail `gorm:"embedded"`
	Stock  int32 `json:"-"`
}

func (Voucher) TableName() string { return "tb_voucher" }

type Order struct {
	ID         int64      `gorm:"primaryKey;autoIncrement:false" json:"id,string"`
	UserID     uint64     `json:"userId"`
	VoucherID  uint64     `json:"voucherId"`
	Amount     uint64     `json:"amount"`
	ReservedAt time.Time  `json:"reservedAt"`
	ExpireAt   time.Time  `json:"expireAt"`
	PayType    uint8      `gorm:"default:1" json:"payType"`
	Status     uint8      `gorm:"default:1" json:"status"`
	CreateTime time.Time  `gorm:"autoCreateTime" json:"createTime"`
	PayTime    *time.Time `json:"payTime,omitempty"`
	UseTime    *time.Time `json:"useTime,omitempty"`
	RefundTime *time.Time `json:"refundTime,omitempty"`
	UpdateTime time.Time  `gorm:"autoUpdateTime" json:"updateTime"`
}

func (Order) TableName() string { return "tb_voucher_order" }
