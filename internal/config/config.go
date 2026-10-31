package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"lifemate/internal/ratelimit"
	"lifemate/internal/voucher"
)

type Config struct {
	HTTPAddr              string
	MySQLDSN              string
	RedisURL              string
	MQNameservers         []string
	MQTopic               string
	MQProducerGroup       string
	MQConsumerGroup       string
	SeckillLimit          ratelimit.Config
	Order                 voucher.OrderConfig
	MQTimeoutTopic        string
	MQScheduleGroup       string
	MQCloseGroup          string
	MQOrderConcurrency    int
	MQScheduleConcurrency int
	MQCloseConcurrency    int
}

func Load() (Config, error) {
	cfg := Config{HTTPAddr: os.Getenv("HTTP_ADDR"), MySQLDSN: os.Getenv("MYSQL_DSN"), RedisURL: os.Getenv("REDIS_URL")}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8081"
	}
	if cfg.MySQLDSN == "" {
		return Config{}, errors.New("请设置 MYSQL_DSN 环境变量，参见 README.md")
	}
	if cfg.RedisURL == "" {
		cfg.RedisURL = "redis://127.0.0.1:16380/0"
	}
	nameservers := envOr("ROCKETMQ_NAMESERVERS", "127.0.0.1:9876")
	for _, address := range strings.Split(nameservers, ";") {
		if address = strings.TrimSpace(address); address != "" {
			cfg.MQNameservers = append(cfg.MQNameservers, address)
		}
	}
	if len(cfg.MQNameservers) == 0 {
		return Config{}, errors.New("ROCKETMQ_NAMESERVERS 不能为空")
	}
	cfg.MQTopic = envOr("ROCKETMQ_TOPIC", "lifemate-seckill-orders-v2")
	cfg.MQProducerGroup = envOr("ROCKETMQ_PRODUCER_GROUP", "lifemate-seckill-producer")
	cfg.MQConsumerGroup = envOr("ROCKETMQ_CONSUMER_GROUP", "lifemate-seckill-consumer-v2")
	cfg.MQTimeoutTopic = envOr("ROCKETMQ_TIMEOUT_TOPIC", "lifemate-seckill-timeouts-v2")
	cfg.MQScheduleGroup = envOr("ROCKETMQ_SCHEDULE_GROUP", "lifemate-seckill-scheduler-v2")
	cfg.MQCloseGroup = envOr("ROCKETMQ_CLOSE_GROUP", "lifemate-seckill-closer-v2")
	if cfg.MQTopic == cfg.MQTimeoutTopic || cfg.MQConsumerGroup == cfg.MQScheduleGroup ||
		cfg.MQConsumerGroup == cfg.MQCloseGroup || cfg.MQScheduleGroup == cfg.MQCloseGroup {
		return Config{}, errors.New("下单和关单须使用不同 Topic，落单/调度/关单须使用三个不同消费者组")
	}
	for _, setting := range []struct {
		name   string
		target *int
	}{
		{"ROCKETMQ_ORDER_CONCURRENCY", &cfg.MQOrderConcurrency}, {"ROCKETMQ_SCHEDULE_CONCURRENCY", &cfg.MQScheduleConcurrency},
		{"ROCKETMQ_CLOSE_CONCURRENCY", &cfg.MQCloseConcurrency},
	} {
		value, err := strconv.Atoi(envOr(setting.name, "4"))
		if err != nil || value < 1 || value > 256 {
			return Config{}, fmt.Errorf("%s 必须为1～256的整数", setting.name)
		}
		*setting.target = value
	}
	cfg.Order = voucher.DefaultOrderConfig()
	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{"ORDER_PAYMENT_TIMEOUT", &cfg.Order.PaymentTimeout}, {"ORDER_PREORDER_GRACE", &cfg.Order.PreorderGrace},
	} {
		if raw, exists := os.LookupEnv(setting.name); exists {
			value, err := time.ParseDuration(strings.TrimSpace(raw))
			if err != nil {
				return Config{}, fmt.Errorf("%s 必须为有效时长: %w", setting.name, err)
			}
			*setting.target = value
		}
	}
	if err := cfg.Order.Validate(); err != nil {
		return Config{}, err
	}
	limits := ratelimit.DefaultConfig()
	if prefix, exists := os.LookupEnv("SECKILL_LIMIT_PREFIX"); exists {
		limits.Prefix = strings.TrimSpace(prefix)
	}
	for _, setting := range []struct {
		name   string
		target *float64
	}{
		{"SECKILL_GLOBAL_RATE", &limits.Global.Rate}, {"SECKILL_USER_RATE", &limits.User.Rate},
	} {
		if raw, exists := os.LookupEnv(setting.name); exists {
			value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil {
				return Config{}, fmt.Errorf("%s 必须是数字: %w", setting.name, err)
			}
			*setting.target = value
		}
	}
	for _, setting := range []struct {
		name   string
		target *int64
	}{
		{"SECKILL_GLOBAL_BURST", &limits.Global.Burst}, {"SECKILL_USER_BURST", &limits.User.Burst},
	} {
		if raw, exists := os.LookupEnv(setting.name); exists {
			value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				return Config{}, fmt.Errorf("%s 必须是整数: %w", setting.name, err)
			}
			*setting.target = value
		}
	}
	if err := limits.Validate(); err != nil {
		return Config{}, err
	}
	cfg.SeckillLimit = limits
	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
