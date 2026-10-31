package ordermq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	rocketmq "github.com/apache/rocketmq-client-go/v2"
	"github.com/apache/rocketmq-client-go/v2/consumer"
	"github.com/apache/rocketmq-client-go/v2/primitive"
	"github.com/apache/rocketmq-client-go/v2/producer"
	"github.com/apache/rocketmq-client-go/v2/rlog"
	"lifemate/internal/voucher"
)

type Config struct {
	Nameservers         []string
	Topic               string
	ProducerGroup       string
	ConsumerGroup       string
	TimeoutTopic        string
	ScheduleGroup       string
	CloseGroup          string
	OrderConcurrency    int
	ScheduleConcurrency int
	CloseConcurrency    int
}

func (c Config) defaults() Config {
	if c.TimeoutTopic == "" {
		c.TimeoutTopic = c.Topic + "-timeout"
	}
	if c.ScheduleGroup == "" {
		c.ScheduleGroup = c.ConsumerGroup + "-schedule"
	}
	if c.CloseGroup == "" {
		c.CloseGroup = c.ConsumerGroup + "-close"
	}
	if c.OrderConcurrency <= 0 {
		c.OrderConcurrency = 4
	}
	if c.ScheduleConcurrency <= 0 {
		c.ScheduleConcurrency = 4
	}
	if c.CloseConcurrency <= 0 {
		c.CloseConcurrency = 4
	}
	return c
}

type Producer struct {
	client       rocketmq.Producer
	topic        string
	timeoutTopic string
}

func NewProducer(cfg Config) (*Producer, error) {
	cfg = cfg.defaults()
	rlog.SetLogLevel("warn")
	client, err := rocketmq.NewProducer(producer.WithNameServer(cfg.Nameservers),
		producer.WithGroupName(cfg.ProducerGroup), producer.WithInstanceName(fmt.Sprintf("producer-%d", time.Now().UnixNano())),
		producer.WithRetry(2), producer.WithVIPChannel(false))
	if err != nil {
		return nil, err
	}
	if err := client.Start(); err != nil {
		_ = client.Shutdown()
		return nil, err
	}
	return &Producer{client: client, topic: cfg.Topic, timeoutTopic: cfg.TimeoutTopic}, nil
}

func (p *Producer) Publish(ctx context.Context, message voucher.OrderMessage) error {
	return p.send(ctx, p.topic, message, false)
}

func (p *Producer) PublishTimeout(ctx context.Context, message voucher.OrderMessage) error {
	return p.send(ctx, p.timeoutTopic, message, true)
}

func (p *Producer) send(ctx context.Context, topic string, message voucher.OrderMessage, timed bool) error {
	if err := message.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	msg := primitive.NewMessage(topic, body)
	msg.WithKeys([]string{strconv.FormatInt(message.ID, 10)})
	if timed {
		// v2 Remoting SDK 使用原生 5.3.2 定时属性；绝对毫秒时间不会随重试延长。
		msg.WithProperty("TIMER_DELIVER_MS", strconv.FormatInt(message.ExpireAt, 10))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := p.client.SendSync(ctx, msg)
	if err != nil {
		return err
	}
	if result == nil || result.Status != primitive.SendOK {
		return fmt.Errorf("MQ 未确认发送成功：%v", result)
	}
	return nil
}

func (p *Producer) Close() error { return p.client.Shutdown() }

type Persister interface {
	PersistOrder(context.Context, voucher.OrderMessage) error
}
type Consumer struct{ client rocketmq.PushConsumer }

func NewConsumer(cfg Config, persister Persister) (*Consumer, error) {
	cfg = cfg.defaults()
	return newConsumer(cfg, cfg.Topic, cfg.ConsumerGroup, cfg.OrderConcurrency, persister.PersistOrder)
}

type TimeoutPublisher interface {
	PublishTimeout(context.Context, voucher.OrderMessage) error
}
type Closer interface {
	CloseExpiredOrder(context.Context, voucher.OrderMessage) error
}

// 独立消费同一个普通下单 Topic，不等待落单组写入 MySQL。
func NewSchedulerConsumer(cfg Config, publisher TimeoutPublisher) (*Consumer, error) {
	cfg = cfg.defaults()
	return newConsumer(cfg, cfg.Topic, cfg.ScheduleGroup, cfg.ScheduleConcurrency, publisher.PublishTimeout)
}

func NewTimeoutConsumer(cfg Config, closer Closer, publisher TimeoutPublisher) (*Consumer, error) {
	cfg = cfg.defaults()
	return newConsumer(cfg, cfg.TimeoutTopic, cfg.CloseGroup, cfg.CloseConcurrency, func(ctx context.Context, m voucher.OrderMessage) error {
		err := closer.CloseExpiredOrder(ctx, m)
		if errors.Is(err, voucher.ErrNotDue) {
			return publisher.PublishTimeout(ctx, m)
		}
		return err
	})
}

func newConsumer(cfg Config, topic, group string, concurrency int, handle func(context.Context, voucher.OrderMessage) error) (*Consumer, error) {
	rlog.SetLogLevel("warn")
	client, err := rocketmq.NewPushConsumer(consumer.WithNameServer(cfg.Nameservers),
		consumer.WithInstance(fmt.Sprintf("consumer-%d", time.Now().UnixNano())),
		consumer.WithGroupName(group), consumer.WithConsumerModel(consumer.Clustering),
		consumer.WithConsumeFromWhere(consumer.ConsumeFromFirstOffset), consumer.WithVIPChannel(false),
		consumer.WithConsumeMessageBatchMaxSize(1), consumer.WithConsumeGoroutineNums(concurrency))
	if err != nil {
		return nil, err
	}
	err = client.Subscribe(topic, consumer.MessageSelector{}, func(ctx context.Context, messages ...*primitive.MessageExt) (consumer.ConsumeResult, error) {
		for _, msg := range messages {
			var order voucher.OrderMessage
			err := json.Unmarshal(msg.Body, &order)
			if err == nil {
				err = order.Validate()
			}
			if err == nil {
				workCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err = handle(workCtx, order)
				cancel()
			}
			if err != nil {
				slog.Error("订单消费失败，交给 MQ 重试", "group", group, "topic", topic, "messageId", msg.MsgId, "orderId", order.ID, "error", err)
				return consumer.ConsumeRetryLater, nil
			}
		}
		return consumer.ConsumeSuccess, nil
	})
	if err != nil {
		return nil, err
	}
	if err := client.Start(); err != nil {
		_ = client.Shutdown()
		return nil, err
	}
	return &Consumer{client: client}, nil
}

func (c *Consumer) Close() error { return c.client.Shutdown() }
