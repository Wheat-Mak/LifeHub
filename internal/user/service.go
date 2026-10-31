package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	CodeTTL          = 2 * time.Minute
	SessionTTL       = 30 * time.Minute
	CodeKeyPrefix    = "login:code:"
	SessionKeyPrefix = "login:token:"
)

var (
	ErrPhone = errors.New("手机号格式错误")
	ErrCode  = errors.New("验证码不一致，请重新输入")
	// 沿用参考工程支持的号段，去掉 Java 正则字符类中多余的逗号。
	phonePattern = regexp.MustCompile(`^1(3[0-9]|4[579]|5[0-35-9]|66|7[0135678]|8[0-9]|9[89])[0-9]{8}$`)
)

type Service struct {
	db    *gorm.DB
	redis *redis.Client
}

func NewService(db *gorm.DB, client *redis.Client) *Service {
	return &Service{db: db, redis: client}
}

// SendCode 返回验证码供 HTTP 层写入开发日志，接口不向调用方返回验证码。
func (s *Service) SendCode(ctx context.Context, phone string) (string, error) {
	if !phonePattern.MatchString(phone) {
		return "", ErrPhone
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", number.Int64())
	err = s.redis.Set(ctx, CodeKeyPrefix+phone, code, CodeTTL).Err()
	return code, err
}

func (s *Service) Login(ctx context.Context, phone, code string) (string, error) {
	if !phonePattern.MatchString(phone) {
		return "", ErrPhone
	}
	db := s.db.WithContext(ctx)
	saved, err := s.redis.Get(ctx, CodeKeyPrefix+phone).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrCode
	}
	if err != nil {
		return "", err
	}
	if saved != code {
		return "", ErrCode
	}
	var u User
	err = db.Where("phone = ?", phone).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		suffix, err := randomHex(5)
		if err != nil {
			return "", err
		}
		u = User{Phone: phone, NickName: "user_" + suffix}
		if err := db.Create(&u).Error; err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	token, err := randomHex(16)
	if err != nil {
		return "", err
	}
	key := SessionKeyPrefix + token
	if err := s.redis.HSet(ctx, key, map[string]any{
		"id": strconv.FormatUint(u.ID, 10), "nickName": u.NickName, "icon": u.Icon,
	}).Err(); err != nil {
		return "", err
	}
	if err := s.redis.Expire(ctx, key, SessionTTL).Err(); err != nil {
		return "", err
	}
	// 与参考实现一致：验证码不消费，Hash 写入与设置 TTL 分步执行。
	return token, nil
}

// Restore 从 Redis 会话快照恢复身份并续期，不查询 MySQL。
func (s *Service) Restore(ctx context.Context, token string) (*UserDTO, error) {
	if len(token) != 32 {
		return nil, nil
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil, nil
	}
	key := SessionKeyPrefix + token
	fields, err := s.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	id, err := strconv.ParseUint(fields["id"], 10, 64)
	if err != nil || id == 0 {
		return nil, errors.New("Redis 会话用户 ID 无效")
	}
	valid, err := s.redis.Expire(ctx, key, SessionTTL).Result()
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, nil
	}
	return &UserDTO{ID: id, NickName: fields["nickName"], Icon: fields["icon"]}, nil
}

func (s *Service) FindByID(ctx context.Context, id uint64) (*UserDTO, error) {
	var u User
	err := s.db.WithContext(ctx).Select("id", "nick_name", "icon").Take(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dto := u.DTO()
	return &dto, nil
}

func (s *Service) FindInfo(ctx context.Context, id uint64) (*UserInfo, error) {
	var info UserInfo
	err := s.db.WithContext(ctx).Take(&info, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &info, err
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
