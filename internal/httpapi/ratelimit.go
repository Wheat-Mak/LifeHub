package httpapi

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"lifemate/internal/ratelimit"
	"lifemate/internal/user"
)

const voucherIDKey = "seckillVoucherID"

func limitSeckill(limiter *ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, valid := userID(c)
		if !valid {
			return
		}
		account := c.MustGet(userKey).(*user.UserDTO)
		decision, err := limiter.Allow(c.Request.Context(), account.ID)
		if err != nil {
			slog.Error("秒杀限流依赖失败", "error", err)
			fail(c, http.StatusServiceUnavailable, "限流服务暂不可用，请稍后再试")
			return
		}
		if !decision.Allowed {
			c.Header("Retry-After", strconv.FormatInt(int64(math.Ceil(decision.RetryAfter.Seconds())), 10))
			message := "系统繁忙，请稍后再试"
			if decision.LimitedBy == ratelimit.User {
				message = "操作过于频繁，请稍后再试"
			}
			fail(c, http.StatusTooManyRequests, message)
			return
		}
		c.Set(voucherIDKey, id)
		c.Next()
	}
}
