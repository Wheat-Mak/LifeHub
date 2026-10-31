package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"lifemate/internal/ratelimit"
	"lifemate/internal/user"
	"lifemate/internal/voucher"
)

func registerVouchers(r *gin.Engine, service *voucher.Service, limiter *ratelimit.Limiter) {
	r.GET("/voucher/:id", func(c *gin.Context) {
		id, valid := userID(c)
		if !valid {
			return
		}
		detail, err := service.FindByID(c.Request.Context(), id)
		switch {
		case errors.Is(err, voucher.ErrUnavailable):
			fail(c, http.StatusOK, err.Error())
		case err != nil:
			serverError(c, err)
		default:
			ok(c, detail)
		}
	})
	r.GET("/voucher/list/:id", func(c *gin.Context) {
		shopID, valid := userID(c)
		if !valid {
			return
		}
		records, err := service.ListOfShop(c.Request.Context(), shopID)
		if err != nil {
			serverError(c, err)
			return
		}
		ok(c, records)
	})
	r.POST("/voucher-order/seckill/:id", requireLogin(), limitSeckill(limiter), func(c *gin.Context) {
		voucherID := c.MustGet(voucherIDKey).(uint64)
		account := c.MustGet(userKey).(*user.UserDTO)
		accepted, err := service.Seckill(c.Request.Context(), account.ID, voucherID)
		switch {
		case errors.Is(err, voucher.ErrUnavailable), errors.Is(err, voucher.ErrNotStarted),
			errors.Is(err, voucher.ErrEnded), errors.Is(err, voucher.ErrOutOfStock),
			errors.Is(err, voucher.ErrDuplicateOrder), errors.Is(err, voucher.ErrNotPrepared):
			fail(c, http.StatusOK, err.Error())
		case errors.Is(err, voucher.ErrInvalidID):
			fail(c, http.StatusBadRequest, err.Error())
		case err != nil:
			serverError(c, err)
		default:
			ok(c, accepted)
		}
	})
	r.POST("/voucher-order/pay/:id", requireLogin(), func(c *gin.Context) {
		id, valid := orderID(c)
		if !valid {
			return
		}
		account := c.MustGet(userKey).(*user.UserDTO)
		if err := service.Pay(c.Request.Context(), account.ID, id); err != nil {
			orderError(c, err)
			return
		}
		ok(c, gin.H{"id": strconv.FormatInt(id, 10), "status": voucher.OrderPaid})
	})
	r.GET("/voucher-order/:id", requireLogin(), func(c *gin.Context) {
		id, valid := orderID(c)
		if !valid {
			return
		}
		account := c.MustGet(userKey).(*user.UserDTO)
		order, err := service.FindOrder(c.Request.Context(), account.ID, id)
		if err != nil {
			orderError(c, err)
			return
		}
		ok(c, order)
	})
}

func orderID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, "订单 ID 必须是正整数且不超过 int64")
		return 0, false
	}
	return id, true
}

func orderError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, voucher.ErrOrderForbidden):
		fail(c, http.StatusForbidden, err.Error())
	case errors.Is(err, voucher.ErrInvalidID):
		fail(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, voucher.ErrOrderNotFound), errors.Is(err, voucher.ErrNotPayable), errors.Is(err, voucher.ErrOutOfStock), errors.Is(err, voucher.ErrInvalidSnapshot):
		fail(c, http.StatusOK, err.Error())
	default:
		serverError(c, err)
	}
}
