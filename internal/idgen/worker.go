package idgen

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

const beginTimestamp int64 = 1640995200 // 2022-01-01 UTC，与参考工程一致。

type Worker struct {
	redis *redis.Client
	now   func() time.Time
}

func NewWorker(client *redis.Client) *Worker {
	return &Worker{redis: client, now: func() time.Time { return time.Now().UTC() }}
}

// Next 只生成订单号，不创建订单；日期和时间戳统一按 UTC 计算。
func (w *Worker) Next(ctx context.Context) (int64, error) {
	now := w.now().UTC()
	seconds := now.Unix() - beginTimestamp
	if seconds < 0 || seconds > math.MaxInt32 {
		return 0, errors.New("订单号时间戳超出范围")
	}
	sequence, err := w.redis.Incr(ctx, "icr:order:"+now.Format("2006:01:02")).Result()
	if err != nil {
		return 0, err
	}
	if sequence <= 0 || sequence > math.MaxUint32 {
		return 0, errors.New("订单号当日序列超出范围")
	}
	return seconds<<32 | sequence, nil
}
