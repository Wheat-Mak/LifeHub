package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lifemate/internal/config"
	"lifemate/internal/database"
	"lifemate/internal/httpapi"
	"lifemate/internal/idgen"
	"lifemate/internal/ordermq"
	"lifemate/internal/ratelimit"
	"lifemate/internal/redisstore"
	"lifemate/internal/shop"
	"lifemate/internal/user"
	"lifemate/internal/voucher"
)

func main() {
	if err := run(); err != nil {
		slog.Error("服务退出", "error", err)
		os.Exit(1)
	}
}

func run() error {
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
	limiter, err := ratelimit.New(client, cfg.SeckillLimit)
	if err != nil {
		return err
	}
	mqConfig := ordermq.Config{Nameservers: cfg.MQNameservers, Topic: cfg.MQTopic,
		ProducerGroup: cfg.MQProducerGroup, ConsumerGroup: cfg.MQConsumerGroup,
		TimeoutTopic: cfg.MQTimeoutTopic, ScheduleGroup: cfg.MQScheduleGroup, CloseGroup: cfg.MQCloseGroup,
		OrderConcurrency: cfg.MQOrderConcurrency, ScheduleConcurrency: cfg.MQScheduleConcurrency, CloseConcurrency: cfg.MQCloseConcurrency}
	producer, err := ordermq.NewProducer(mqConfig)
	if err != nil {
		return err
	}
	defer producer.Close()
	vouchers, err := voucher.NewServiceWithOrderConfig(db, client, idgen.NewWorker(client), producer, cfg.Order)
	if err != nil {
		return err
	}
	consumer, err := ordermq.NewConsumer(mqConfig, vouchers)
	if err != nil {
		return err
	}
	defer consumer.Close()
	scheduler, err := ordermq.NewSchedulerConsumer(mqConfig, producer)
	if err != nil {
		return err
	}
	defer scheduler.Close()
	closer, err := ordermq.NewTimeoutConsumer(mqConfig, vouchers, producer)
	if err != nil {
		return err
	}
	defer closer.Close()
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: httpapi.NewRouter(user.NewService(db, client), shop.NewService(db, client),
			vouchers, limiter),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errorsCh := make(chan error, 1)
	go func() {
		slog.Info("LifeMate 服务启动", "addr", cfg.HTTPAddr)
		errorsCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
	return nil
}
