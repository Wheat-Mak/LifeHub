package user

import "time"

type User struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	Phone      string
	Password   string
	NickName   string
	Icon       string
	CreateTime time.Time `gorm:"autoCreateTime"`
	UpdateTime time.Time `gorm:"autoUpdateTime"`
}

func (User) TableName() string { return "tb_user" }

// UserDTO 与参考工程的对外用户信息一致，不暴露手机号、密码。
type UserDTO struct {
	ID       uint64 `json:"id"`
	NickName string `json:"nickName"`
	Icon     string `json:"icon"`
}

func (u User) DTO() UserDTO {
	return UserDTO{ID: u.ID, NickName: u.NickName, Icon: u.Icon}
}

type UserInfo struct {
	UserID    uint64     `gorm:"primaryKey;autoIncrement:false" json:"userId"`
	City      *string    `json:"city,omitempty"`
	Introduce *string    `json:"introduce,omitempty"`
	Fans      *uint32    `json:"fans,omitempty"`
	Followee  *uint32    `json:"followee,omitempty"`
	Gender    *bool      `json:"gender,omitempty"`
	Birthday  *time.Time `json:"-"`
	Credits   *uint32    `json:"credits,omitempty"`
	Level     *uint8     `json:"level,omitempty"`
}

func (UserInfo) TableName() string { return "tb_user_info" }
