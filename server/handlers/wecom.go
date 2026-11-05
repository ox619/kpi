package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"dootask-kpi-server/models"
	"dootask-kpi-server/utils/wecom"
)

// 企业微信 OAuth 相关常量。
const (
	// wecomOAuthStateCookie state 校验 cookie 名，用于防 CSRF。
	wecomOAuthStateCookie = "wecom_oauth_state"
	// wecomOAuthStateMaxAge state 有效期（秒），与企业微信授权码 5 分钟有效期对齐。
	wecomOAuthStateMaxAge = 600

	// wecomBindStatePrefix 绑定 state 前缀，用来和登录模式使用的随机 state 区分。
	wecomBindStatePrefix = "b."
	wecomBindStateCookie = "wecom_bind_nonce"
	// wecomBindStateTTL 绑定 state 有效期。
	wecomBindStateTTL = 10 * time.Minute
	// wecomLoginTicketTTL 登录一次性 ticket 有效期。
	wecomLoginTicketTTL = 5 * time.Minute
)

// WeComStatusHandler 企业微信接入状态（公开，始终注册）。
// 未配置 WECOM_* 时静默返回 enabled=false，前端据此决定是否展示「企业微信登录」入口，
// 避免未配置时出现 404/报错，对现有站内登录零影响。
func WeComStatusHandler(c *gin.Context) {
	client := wecom.Current()
	enabled := client != nil
	c.JSON(http.StatusOK, gin.H{
		"enabled":          enabled,
		"callback_enabled": enabled && client.Config().CallbackEnabled(),
	})
}

// WeComOAuthHandler 发起企业微信网页授权（登录模式）：重定向到企微授权链接。
// 仅在 WECOM_* 配置完整（wecom.Current() != nil）时注册，否则该路由不存在，完全静默降级。
func WeComOAuthHandler(c *gin.Context) {
	client := wecom.Current()
	if client == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "企业微信接入未启用"})
		return
	}

	// 随机 state 防 CSRF，回调时与 cookie 比对。
	state, err := randomState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成授权状态失败"})
		return
	}
	c.SetCookie(wecomOAuthStateCookie, state, wecomOAuthStateMaxAge, "/", "", false, true)

	redirectURI := wecomRedirectURI(c, client.Config().BaseURL)
	if redirectURI == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "未配置企业微信回调地址（WECOM_BASE_URL）"})
		return
	}
	c.Redirect(http.StatusFound, client.OAuthURL(redirectURI, state))
}

// WeComBindStartHandler 登录后发起企业微信绑定（需要 JWT）。
// 后端只返回企微授权链接，页面跳转和结果展示全部由前端负责。
func WeComBindStartHandler(c *gin.Context) {
	client := wecom.Current()
	if client == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "企业微信接入未启用"})
		return
	}

	employeeID := c.GetUint("user_id")
	if employeeID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户未登录"})
		return
	}

	var user models.Employee
	if err := models.DB.Select("id", "is_active", "wecom_user_id", "wecom_auth_version").First(&user, employeeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		log.Printf("[wecom] 查询待绑定账号失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询账号失败，请稍后重试"})
		return
	}
	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "账号已被禁用，无法绑定企业微信"})
		return
	}
	if user.WeComUserID != nil && strings.TrimSpace(*user.WeComUserID) != "" {
		c.JSON(http.StatusConflict, gin.H{"error": "该账号已绑定企业微信，如需更换请先在设置页解绑"})
		return
	}

	// 绑定完成后的跳转目标是前端设置页，必须知道前端访问地址。
	// 这里显式要求 WECOM_BASE_URL，避免在直连后端端口调试时跳到错误的主机。
	if strings.TrimSpace(client.Config().BaseURL) == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "未配置 WECOM_BASE_URL，无法发起企业微信绑定"})
		return
	}
	redirectURI := wecomRedirectURI(c, client.Config().BaseURL)
	if redirectURI == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "未配置企业微信回调地址（WECOM_BASE_URL）"})
		return
	}

	// 一次性 nonce 同时关联已登录账号和发起浏览器，不能仅凭转发的授权链接绑定。
	state, nonce, err := issueWeComBindState(user.ID, user.WeComAuthVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成绑定状态失败，请重试"})
		return
	}
	setWeComBindCookie(c, nonce, int(wecomBindStateTTL.Seconds()))
	c.JSON(http.StatusOK, gin.H{"url": client.OAuthURL(redirectURI, state)})
}

// WeComUnbindHandler 只允许当前用户用本地密码确认解绑，不依赖企微服务是否启用。
func WeComUnbindHandler(c *gin.Context) {
	employeeID := c.GetUint("user_id")
	if employeeID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户未登录"})
		return
	}
	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入当前账号密码"})
		return
	}
	var user models.Employee
	if err := models.DB.First(&user, employeeID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取账号失败，请稍后重试"})
		return
	}
	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "账号已被禁用"})
		return
	}
	// 密码错误用 400，避免触发全局 401 登出。
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "密码错误，请输入当前 KPI 账号的密码"})
		return
	}
	// 版本号防止已进入回调、尚未完成写库的旧请求在解绑后重新绑定。
	res := models.DB.Model(&models.Employee{}).
		Where("id = ? AND is_active = ? AND password = ?", employeeID, true, user.Password).
		Updates(map[string]interface{}{
			"wecom_user_id":      nil,
			"wecom_auth_version": gorm.Expr("wecom_auth_version + 1"),
		})
	if res.Error != nil || res.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "账号状态已变化或解绑失败，请刷新后重试"})
		return
	}
	revokeWeComPendingAuth(employeeID)
	setWeComBindCookie(c, "", -1)
	log.Printf("[wecom] 自助解绑成功：本地员工 id=%d", employeeID)
	c.JSON(http.StatusOK, gin.H{"message": "企业微信已解绑"})
}

func revokeWeComPendingAuth(employeeID uint) {
	wecomBindRequests.Lock()
	for key, item := range wecomBindRequests.items {
		if item.employeeID == employeeID {
			delete(wecomBindRequests.items, key)
		}
	}
	wecomBindRequests.Unlock()
	wecomLoginTickets.Lock()
	for key, item := range wecomLoginTickets.items {
		if item.employeeID == employeeID {
			delete(wecomLoginTickets.items, key)
		}
	}
	wecomLoginTickets.Unlock()
}

// WeComBindingStatusHandler 返回当前账号的企业微信绑定状态（需要 JWT）。
func WeComBindingStatusHandler(c *gin.Context) {
	employeeID := c.GetUint("user_id")
	if employeeID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户未登录"})
		return
	}

	var user models.Employee
	if err := models.DB.Select("id", "wecom_user_id").First(&user, employeeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		log.Printf("[wecom] 查询绑定状态失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询绑定状态失败"})
		return
	}

	resp := gin.H{
		"enabled": wecom.Current() != nil,
		"bound":   false,
	}
	if user.WeComUserID != nil && strings.TrimSpace(*user.WeComUserID) != "" {
		resp["bound"] = true
		resp["wecom_user_id"] = *user.WeComUserID
	}
	c.JSON(http.StatusOK, resp)
}

// WeComCallbackHandler 企业微信网页授权回调。
//
// 两种模式：
//   - 绑定模式：签名 state + 浏览器 cookie + 服务端一次性记录；
//   - 登录模式：随机 state + cookie 校验，按 WeComUserID 查找已绑定员工。
//
// 回调只做 302 跳转，不输出任何 HTML；结果由前端页面展示。
func WeComCallbackHandler(c *gin.Context) {
	client := wecom.Current()
	if client == nil {
		redirectWeCom(c, wecomFrontendBase(c, "")+"/auth/login?wecom=error")
		return
	}

	base := wecomFrontendBase(c, client.Config().BaseURL)
	state := c.Query("state")

	if strings.HasPrefix(state, wecomBindStatePrefix) {
		handleWeComBindCallback(c, client, base, state)
		return
	}

	// 登录模式：校验 state cookie 防 CSRF；无论成败都作废该 cookie，避免被重放。
	expectedState, _ := c.Cookie(wecomOAuthStateCookie)
	c.SetCookie(wecomOAuthStateCookie, "", -1, "/", "", false, true)
	if expectedState == "" || state == "" || expectedState != state {
		redirectWeCom(c, base+"/auth/login?wecom=error")
		return
	}

	code := c.Query("code")
	if code == "" {
		redirectWeCom(c, base+"/auth/login?wecom=error")
		return
	}

	userID, err := client.UserIDByCode(code)
	if err != nil {
		log.Printf("[wecom] OAuth code 换取 userid 失败：%v", err)
		redirectWeCom(c, base+"/auth/login?wecom=error")
		return
	}

	var user models.Employee
	err = models.DB.Preload("Department").Where("wecom_user_id = ?", userID).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[wecom] 企业微信登录未绑定：UserID=%s", userID)
		redirectWeCom(c, base+"/auth/login?wecom=unbound")
		return
	}
	if err != nil {
		log.Printf("[wecom] 查询企业微信绑定失败：%v", err)
		redirectWeCom(c, base+"/auth/login?wecom=error")
		return
	}
	if !user.IsActive {
		redirectWeCom(c, base+"/auth/login?wecom=disabled")
		return
	}

	// 不把 JWT 放进 URL，改为一次性 ticket，由前端调 /auth/wecom/exchange 换取登录态。
	ticket, err := issueWeComLoginTicket(user.ID, user.WeComAuthVersion)
	if err != nil {
		log.Printf("[wecom] 生成登录 ticket 失败：%v", err)
		redirectWeCom(c, base+"/auth/login?wecom=error")
		return
	}

	redirectWeCom(c, base+"/auth/wecom/callback?ticket="+url.QueryEscape(ticket))
}

// handleWeComBindCallback 处理登录后的绑定回调。
func handleWeComBindCallback(c *gin.Context, client *wecom.Client, base, state string) {
	nonce, _ := c.Cookie(wecomBindStateCookie)
	setWeComBindCookie(c, "", -1)
	binding, ok := consumeWeComBindState(state, nonce)
	if !ok {
		redirectWeCom(c, base+"/settings?wecom=browser")
		return
	}

	code := c.Query("code")
	if code == "" {
		redirectWeCom(c, base+"/settings?wecom=error")
		return
	}

	userID, err := client.UserIDByCode(code)
	if err != nil {
		log.Printf("[wecom] 绑定回调换取 userid 失败：%v", err)
		redirectWeCom(c, base+"/settings?wecom=error")
		return
	}

	// 绑定前确认企微成员可用，同时拿到姓名用于日志。
	member, err := client.GetUser(userID)
	if err != nil {
		log.Printf("[wecom] 绑定前读取企微成员失败：%v", err)
		redirectWeCom(c, base+"/settings?wecom=error")
		return
	}
	// status: 2=已禁用，4=未激活（与 utils/wecom/sync.go 的定义一致）。
	if !member.IsEnabled || member.Status == 2 || member.Status == 4 {
		redirectWeCom(c, base+"/settings?wecom=disabled")
		return
	}

	user, err := bindWeComUser(binding.employeeID, userID, binding.authVersion)
	if err != nil {
		switch {
		case errors.Is(err, errWeComBindConflict), errors.Is(err, errWeComBindAccountBound):
			redirectWeCom(c, base+"/settings?wecom=conflict")
		case errors.Is(err, errWeComBindAccountDisabled):
			redirectWeCom(c, base+"/settings?wecom=disabled")
		default:
			log.Printf("[wecom] 自助绑定失败：%v", err)
			redirectWeCom(c, base+"/settings?wecom=error")
		}
		return
	}

	log.Printf("[wecom] 自助绑定成功：企微 UserID=%s（%s）→ 本地员工 id=%d name=%s",
		userID, member.Name, user.ID, user.Name)
	redirectWeCom(c, base+"/settings?wecom=bound")
}

// WeComExchangeHandler 用一次性 ticket 换取 JWT（公开）。
// ticket 由登录回调签发，只能使用一次，避免把 JWT 暴露在浏览器地址栏。
func WeComExchangeHandler(c *gin.Context) {
	var req struct {
		Ticket string `json:"ticket"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效"})
		return
	}

	ticket, ok := consumeWeComLoginTicket(req.Ticket)
	if !ok {
		// 用 400 而不是 401，避免触发前端全局「登录失效」处理。
		c.JSON(http.StatusBadRequest, gin.H{"error": "登录链接已失效，请重新登录"})
		return
	}

	var user models.Employee
	if err := models.DB.Preload("Department").Where("wecom_auth_version = ? AND wecom_user_id IS NOT NULL AND wecom_user_id <> ''", ticket.authVersion).First(&user, ticket.employeeID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "登录链接已失效，请重新登录"})
		return
	}
	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "账号已被禁用，请联系管理员"})
		return
	}

	token, err := generateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "登录态签发失败，请稍后重试"})
		return
	}

	log.Printf("[wecom] 企业微信登录成功：本地员工 id=%d name=%s", user.ID, user.Name)
	c.JSON(http.StatusOK, LoginResponse{Token: token, User: &user})
}

// ---------------------------------------------------------------------------
// 绑定写库
// ---------------------------------------------------------------------------

var (
	errWeComBindConflict        = errors.New("wecom: 企业微信身份已被其他账号绑定")
	errWeComBindAccountBound    = errors.New("wecom: 账号已绑定其他企业微信")
	errWeComBindAccountDisabled = errors.New("wecom: 账号已禁用")
)

// bindWeComUser 把企微 UserID 写到指定本地员工。
// 约束：一个本地账号只能绑定一个企微身份，一个企微身份也只能绑定一个本地账号。
func bindWeComUser(employeeID uint, wecomUserID string, authVersion uint) (*models.Employee, error) {
	var user models.Employee
	if err := models.DB.Preload("Department").First(&user, employeeID).Error; err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, errWeComBindAccountDisabled
	}

	if user.WeComAuthVersion != authVersion {
		return nil, errWeComBindConflict
	}

	if user.WeComUserID != nil && strings.TrimSpace(*user.WeComUserID) != "" {
		if *user.WeComUserID == wecomUserID {
			return &user, nil
		}
		return nil, errWeComBindAccountBound
	}

	var conflict models.Employee
	err := models.DB.Where("wecom_user_id = ? AND id <> ?", wecomUserID, user.ID).First(&conflict).Error
	if err == nil {
		return nil, errWeComBindConflict
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// 条件更新防并发：只有该员工仍未绑定、且该企微身份未被其他员工占用时才写入。
	res := models.DB.Model(&models.Employee{}).
		Where("id = ? AND (wecom_user_id IS NULL OR wecom_user_id = '' OR wecom_user_id = ?)", user.ID, wecomUserID).
		Where("is_active = ? AND wecom_auth_version = ?", true, authVersion).
		Where("NOT EXISTS (SELECT 1 FROM employees WHERE wecom_user_id = ? AND id <> ?)", wecomUserID, user.ID).
		Update("wecom_user_id", wecomUserID)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		// 并发场景：可能刚被其他请求绑定，重新确认归属。
		var bound models.Employee
		if err := models.DB.First(&bound, user.ID).Error; err != nil {
			return nil, err
		}
		if !bound.IsActive || bound.WeComAuthVersion != authVersion || bound.WeComUserID == nil || *bound.WeComUserID != wecomUserID {
			return nil, errWeComBindConflict
		}
		user = bound
	}

	if err := models.DB.Preload("Department").First(&user, user.ID).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// ---------------------------------------------------------------------------
// 绑定 state：签名 nonce，关联浏览器并在回调时原子消费
// ---------------------------------------------------------------------------

type wecomBindRequest struct {
	employeeID  uint
	authVersion uint
	expiresAt   time.Time
}

var wecomBindRequests = struct {
	sync.Mutex
	items map[string]wecomBindRequest
}{
	items: make(map[string]wecomBindRequest),
}

func signWeComBindNonce(nonce string) string {
	mac := hmac.New(sha256.New, append([]byte("wecom-state:"), jwtSecret...))
	mac.Write([]byte(nonce))
	return wecomBindStatePrefix + nonce + "." + hex.EncodeToString(mac.Sum(nil))
}

func issueWeComBindState(employeeID, authVersion uint) (string, string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	nonce := hex.EncodeToString(b)
	now := time.Now()

	wecomBindRequests.Lock()
	defer wecomBindRequests.Unlock()
	for key, item := range wecomBindRequests.items {
		if !now.Before(item.expiresAt) {
			delete(wecomBindRequests.items, key)
		}
	}
	wecomBindRequests.items[nonce] = wecomBindRequest{
		employeeID:  employeeID,
		authVersion: authVersion,
		expiresAt:   now.Add(wecomBindStateTTL),
	}
	return signWeComBindNonce(nonce), nonce, nil
}

func consumeWeComBindState(state, cookieNonce string) (wecomBindRequest, bool) {
	// 先验证浏览器关联和签名，再消费记录，防止转发链接作废他人的绑定请求。
	if cookieNonce == "" || !hmac.Equal([]byte(state), []byte(signWeComBindNonce(cookieNonce))) {
		return wecomBindRequest{}, false
	}
	wecomBindRequests.Lock()
	defer wecomBindRequests.Unlock()
	item, ok := wecomBindRequests.items[cookieNonce]
	if !ok {
		return wecomBindRequest{}, false
	}
	delete(wecomBindRequests.items, cookieNonce)
	if !time.Now().Before(item.expiresAt) {
		return wecomBindRequest{}, false
	}
	return item, true
}

func setWeComBindCookie(c *gin.Context, nonce string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetCookie(wecomBindStateCookie, nonce, maxAge, "/", "", secure, true)
}

// ---------------------------------------------------------------------------

// 登录 ticket：回调 → 前端换取 JWT，一次性使用
// ---------------------------------------------------------------------------

type wecomLoginTicket struct {
	employeeID  uint
	authVersion uint
	expiresAt   time.Time
}

var wecomLoginTickets = struct {
	sync.Mutex
	items map[string]wecomLoginTicket
}{
	items: make(map[string]wecomLoginTicket),
}

func issueWeComLoginTicket(employeeID, authVersion uint) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ticket := hex.EncodeToString(b)

	now := time.Now()
	wecomLoginTickets.Lock()
	defer wecomLoginTickets.Unlock()

	for key, item := range wecomLoginTickets.items {
		if now.After(item.expiresAt) {
			delete(wecomLoginTickets.items, key)
		}
	}
	wecomLoginTickets.items[ticket] = wecomLoginTicket{
		employeeID:  employeeID,
		authVersion: authVersion,
		expiresAt:   now.Add(wecomLoginTicketTTL),
	}

	return ticket, nil
}

func consumeWeComLoginTicket(ticket string) (wecomLoginTicket, bool) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return wecomLoginTicket{}, false
	}

	now := time.Now()
	wecomLoginTickets.Lock()
	defer wecomLoginTickets.Unlock()

	item, ok := wecomLoginTickets.items[ticket]
	if !ok {
		return wecomLoginTicket{}, false
	}
	// 无论是否过期都删除，保证一次性。
	delete(wecomLoginTickets.items, ticket)
	if now.After(item.expiresAt) {
		return wecomLoginTicket{}, false
	}

	return item, true
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

// wecomRedirectURI 拼接 OAuth 回调地址。
// 优先使用 WECOM_BASE_URL（企微后台配置的可信域名）；未配置时按当前请求推导，
// 便于内网/本机调试场景使用。
func wecomRedirectURI(c *gin.Context, baseURL string) string {
	base := wecomFrontendBase(c, baseURL)
	if base == "" {
		return ""
	}
	return base + "/api/auth/wecom/callback"
}

// wecomFrontendBase 返回前端访问基地址（可能带 basePath）。
func wecomFrontendBase(c *gin.Context, configured string) string {
	if configured != "" {
		return strings.TrimRight(configured, "/")
	}
	if base := c.GetString("base_url"); base != "" {
		return strings.TrimRight(base, "/")
	}
	return ""
}

func redirectWeCom(c *gin.Context, target string) {
	c.Redirect(http.StatusFound, target)
}

// randomState 生成 16 位十六进制随机串，用于 OAuth state 防 CSRF。
func randomState() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
