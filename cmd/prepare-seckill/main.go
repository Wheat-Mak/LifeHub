package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"lifemate/internal/config"
	"lifemate/internal/database"
	"lifemate/internal/redisstore"
	"lifemate/internal/voucher"
)

func main() {
	id := flag.Uint64("id", 0, "需要准备的优惠券 ID；仅在停止活动流量且没有待消费消息时执行")
	flag.Parse()
	if err := prepare(*id); err != nil {
		slog.Error("秒杀准备失败", "error", err)
		os.Exit(1)
	}
	slog.Info("秒杀数据已准备，已有库存不会被覆盖", "voucherId", *id)
}

func prepare(id uint64) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.MySQLDSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	client, err := redisstore.Open(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return voucher.NewService(db, client, nil, nil).Prepare(ctx, id)
}
