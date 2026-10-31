package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"lifemate/internal/ratelimit"
	"lifemate/internal/shop"
	"lifemate/internal/user"
	"lifemate/internal/voucher"
)

const userKey = "loginUser"

type result struct {
	Success  bool   `json:"success"`
	ErrorMsg string `json:"errorMsg,omitempty"`
	Data     any    `json:"data,omitempty"`
}

func NewRouter(service *user.Service, shops *shop.Service, vouchers *voucher.Service, limiter *ratelimit.Limiter) *gin.Engine {
	if limiter == nil {
		panic("秒杀路由必须配置限流器")
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), restoreSession(service))
	_ = r.SetTrustedProxies(nil)
	h := handler{users: service}
	r.POST("/user/code", h.sendCode)
	r.POST("/user/login", h.login)
	registerShops(r, shops)
	registerVouchers(r, vouchers, limiter)
	protected := r.Group("/user", requireLogin())
	protected.GET("/me", h.me)
	protected.GET("/info/:id", h.info)
	protected.GET("/:id", h.find)
	protected.POST("/logout", func(c *gin.Context) {
		// 保留参考工程的未完成行为，不删除会话。
		c.Set(userKey, nil)
		fail(c, http.StatusOK, "功能未完成")
	})
	return r
}

func restoreSession(service *user.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 原工程直接传 token；同时接受常用的 Bearer token 写法。
		token := strings.TrimSpace(c.GetHeader("Authorization"))
		if len(token) >= 7 && strings.EqualFold(token[:7], "Bearer ") {
			token = strings.TrimSpace(token[7:])
		}
		if token != "" {
			u, err := service.Restore(c.Request.Context(), token)
			if err != nil {
				serverError(c, err)
				return
			}
			if u != nil {
				c.Set(userKey, u)
			}
		}
		c.Next()
	}
}

func requireLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, exists := c.Get(userKey); !exists {
			fail(c, http.StatusUnauthorized, "请先登录")
			return
		}
		c.Next()
	}
}

type handler struct{ users *user.Service }

func (h handler) sendCode(c *gin.Context) {
	phone := c.Query("phone")
	if phone == "" {
		phone = c.PostForm("phone")
	}
	code, err := h.users.SendCode(c.Request.Context(), phone)
	if err != nil {
		handleError(c, err)
		return
	}
	// 与参考工程一致，仅用于本地联调，未接入短信平台。
	slog.Info("短信验证码（本地联调）", "phone", phone, "code", code)
	ok(c, nil)
}

func (h handler) login(c *gin.Context) {
	var form struct {
		Phone    string `json:"phone"`
		Code     string `json:"code"`
		Password string `json:"password"` // 保留字段兼容性，参考实现不支持密码登录。
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		fail(c, http.StatusBadRequest, "请求体必须是合法的 JSON，phone 和 code 应为字符串")
		return
	}
	token, err := h.users.Login(c.Request.Context(), form.Phone, form.Code)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, token)
}

func (h handler) me(c *gin.Context) {
	u, _ := c.Get(userKey)
	ok(c, u)
}

func (h handler) find(c *gin.Context) {
	id, valid := userID(c)
	if !valid {
		return
	}
	u, err := h.users.FindByID(c.Request.Context(), id)
	if err != nil {
		serverError(c, err)
		return
	}
	if u == nil {
		ok(c, nil)
		return
	}
	ok(c, u)
}

func (h handler) info(c *gin.Context) {
	id, valid := userID(c)
	if !valid {
		return
	}
	info, err := h.users.FindInfo(c.Request.Context(), id)
	if err != nil {
		serverError(c, err)
		return
	}
	if info == nil {
		ok(c, nil)
		return
	}
	// LocalDate 的 JSON 为日期字符串；GORM 将 MySQL DATE 读为 time.Time。
	var birthday *string
	if info.Birthday != nil {
		date := info.Birthday.Format("2006-01-02")
		birthday = &date
	}
	ok(c, struct {
		*user.UserInfo
		Birthday *string `json:"birthday,omitempty"`
	}{info, birthday})
}

func userID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, "ID 必须是正整数")
		return 0, false
	}
	return id, true
}

func handleError(c *gin.Context, err error) {
	if errors.Is(err, user.ErrPhone) || errors.Is(err, user.ErrCode) {
		// 参考工程的业务失败使用 HTTP 200，由 success 区分。
		fail(c, http.StatusOK, err.Error())
		return
	}
	serverError(c, err)
}

func serverError(c *gin.Context, err error) {
	slog.Error("数据库或内部操作失败", "error", err)
	fail(c, http.StatusInternalServerError, "服务异常，请稍后重试")
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, result{Success: true, Data: data})
}

func fail(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, result{Success: false, ErrorMsg: message})
}
