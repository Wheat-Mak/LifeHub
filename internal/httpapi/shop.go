package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lifemate/internal/shop"
)

func registerShops(r *gin.Engine, service *shop.Service) {
	r.PUT("/shop", requireLogin(), func(c *gin.Context) {
		var input shop.UpdateInput
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, "请求体必须是合法的店铺更新 JSON")
			return
		}
		err := service.Update(c.Request.Context(), input)
		var invalidation *shop.InvalidationError
		switch {
		case errors.Is(err, shop.ErrInvalidUpdate):
			fail(c, http.StatusBadRequest, "店铺 ID 或更新字段无效")
		case errors.Is(err, shop.ErrNotFound):
			fail(c, http.StatusOK, "店铺不存在！")
		case errors.As(err, &invalidation):
			slog.Error("店铺已提交但缓存失效失败", "shopId", input.ID, "error", err)
			fail(c, http.StatusInternalServerError, "店铺已更新，但缓存失效失败")
		case err != nil:
			serverError(c, err)
		default:
			ok(c, nil)
		}
	})
	r.GET("/shop/:id", func(c *gin.Context) {
		id, valid := userID(c)
		if !valid {
			return
		}
		record, err := service.FindByID(c.Request.Context(), id)
		if err != nil {
			serverError(c, err)
			return
		}
		if record == nil {
			fail(c, http.StatusOK, "店铺不存在！")
			return
		}
		ok(c, record)
	})
	r.GET("/shop/of/name", func(c *gin.Context) {
		current, valid := page(c)
		if !valid {
			return
		}
		records, err := service.List(c.Request.Context(), strings.TrimSpace(c.Query("name")), 0, current, 10)
		if err != nil {
			serverError(c, err)
			return
		}
		ok(c, records)
	})
	r.GET("/shop/of/type", func(c *gin.Context) {
		current, valid := page(c)
		if !valid {
			return
		}
		typeID, err := strconv.ParseUint(c.Query("typeId"), 10, 64)
		if err != nil || typeID == 0 {
			fail(c, http.StatusBadRequest, "商铺类型 ID 必须是正整数")
			return
		}
		if _, x := c.GetQuery("x"); x {
			fail(c, http.StatusBadRequest, "暂不支持附近商铺查询")
			return
		}
		if _, y := c.GetQuery("y"); y {
			fail(c, http.StatusBadRequest, "暂不支持附近商铺查询")
			return
		}
		records, err := service.List(c.Request.Context(), "", typeID, current, 5)
		if err != nil {
			serverError(c, err)
			return
		}
		ok(c, records)
	})
}

func page(c *gin.Context) (int, bool) {
	current, err := strconv.Atoi(c.DefaultQuery("current", "1"))
	// 限定上界也避免分页 offset 的整数溢出。
	if err != nil || current <= 0 || current > 1000000 {
		fail(c, http.StatusBadRequest, "页码必须是 1 到 1000000 的整数")
		return 0, false
	}
	return current, true
}
