package shop

import "time"

type Shop struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name       string     `json:"name"`
	TypeID     uint64     `json:"typeId"`
	Images     string     `json:"images"`
	Area       *string    `json:"area,omitempty"`
	Address    string     `json:"address"`
	X          float64    `json:"x"`
	Y          float64    `json:"y"`
	AvgPrice   *uint64    `json:"avgPrice,omitempty"`
	Sold       uint32     `json:"sold"`
	Comments   uint32     `json:"comments"`
	Score      uint32     `json:"score"`
	OpenHours  *string    `json:"openHours,omitempty"`
	CreateTime *time.Time `gorm:"autoCreateTime" json:"createTime,omitempty"`
	UpdateTime *time.Time `gorm:"autoUpdateTime" json:"updateTime,omitempty"`
}

func (Shop) TableName() string { return "tb_shop" }
