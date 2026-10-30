package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"dootask-kpi-server/models"
	"dootask-kpi-server/utils/wecom"
)

// 企业微信 OAuth 免登相关常量。
const (
	// wecomOAuthStateCookie state 校验 cookie 名，用于防 CSRF。
	wecomOAuthStateCookie = "wecom_oauth_state"
	// wecomOAuthStateMaxAge state 有效期（秒），与企业微信授权码 5 分钟有效期对齐。
	wecomOAuthStateMaxAge = 600
	// wecomLoginSuccessRedirect 登录成功后跳转的前端首页。
	wecomLoginSuccessRedirect = "/evaluations"
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

// WeComOAuthHandler 发起企业微信网页授权：重定向到企微授权链接。
// 仅在 WECOM_* 配置完整（wecom.Current() != nil）时注册，否则该路由不存在，完全静默降级。
// 参考 server/cmd/wecomcheck 的 -oauth 用法：静默授权 snsapi_base，回调换 userid。
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
	c.Redirect(http.StatusFound, client.OAuthURL(redirectURI, state))
}

// WeComCallbackHandler 企业微信网页授权回调：code 换 userid，按 WeComUserID 匹配本地员工完成登录。
// 命中员工：复用现有 JWT 签发机制生成 token，写入前端一致的 localStorage 后跳转首页；
// 未命中：给出可读的绑定提示页。
func WeComCallbackHandler(c *gin.Context) {
	client := wecom.Current()
	if client == nil {
		renderWeComPage(c, http.StatusNotFound, "企业微信接入未启用",
			"企业微信接入未启用，请联系管理员配置 WECOM_* 环境变量后重试。")
		return
	}

	// 校验 state 防 CSRF；无论成败都作废该 cookie，避免被重放。
	expectedState, _ := c.Cookie(wecomOAuthStateCookie)
	c.SetCookie(wecomOAuthStateCookie, "", -1, "/", "", false, true)
	if expectedState == "" || expectedState != c.Query("state") {
		renderWeComPage(c, http.StatusBadRequest, "授权校验失败",
			"授权请求校验失败（state 不匹配），请回到登录页重新发起企业微信登录。")
		return
	}

	code := c.Query("code")
	if code == "" {
		renderWeComPage(c, http.StatusBadRequest, "缺少授权码",
			"未收到企业微信授权码，请回到登录页重新发起企业微信登录。")
		return
	}

	userID, err := client.UserIDByCode(code)
	if err != nil {
		log.Printf("[wecom] OAuth code 换取 userid 失败：%v", err)
		renderWeComPage(c, http.StatusBadRequest, "登录失败",
			"企业微信身份校验失败："+err.Error()+"，请稍后重试或联系管理员。")
		return
	}

	// 用企微 UserID 匹配本地员工（Employee.WeComUserID 已在模型中预留）。
	var user models.Employee
	if err := models.DB.Preload("Department").Where("wecom_user_id = ?", userID).First(&user).Error; err != nil {
		renderWeComPage(c, http.StatusOK, "尚未绑定账号",
			"该企业微信账号（UserID: "+userID+"）尚未绑定到本系统的员工账号。"+
				"请先用邮箱密码登录，并联系 HR 在「员工管理」中填写该员工的「企业微信 UserID」完成绑定。")
		return
	}

	if !user.IsActive {
		renderWeComPage(c, http.StatusOK, "账号已禁用",
			"您的账号已被禁用，无法登录，请联系管理员。")
		return
	}

	// 复用现有登录态签发机制。
	token, err := generateToken(&user)
	if err != nil {
		renderWeComPage(c, http.StatusInternalServerError, "登录失败",
			"登录态签发失败，请稍后重试。")
		return
	}

	// 与前端 authApi.setAuth 一致：写入 auth_token 与 user_info 两个 localStorage 键，
	// 页面初始化时 auth-context 会调 /auth/me 自动校验并恢复登录态。
	userJSON, err := json.Marshal(user)
	if err != nil {
		renderWeComPage(c, http.StatusInternalServerError, "登录失败",
			"登录态序列化失败，请稍后重试。")
		return
	}
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		renderWeComPage(c, http.StatusInternalServerError, "登录失败",
			"登录态序列化失败，请稍后重试。")
		return
	}

	html := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>登录成功</title>
<style>
  body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif;background:#f5f5f5;color:#333;display:flex;align-items:center;justify-content:center;min-height:100vh}
  .box{background:#fff;border-radius:12px;padding:32px 40px;text-align:center;box-shadow:0 4px 24px rgba(0,0,0,.08);max-width:420px}
  .spinner{width:36px;height:36px;margin:0 auto 16px;border:3px solid #e5e7eb;border-top-color:#2563eb;border-radius:50%;animation:spin 1s linear infinite}
  @keyframes spin{to{transform:rotate(360deg)}}
</style>
</head>
<body>
<div class="box">
  <div class="spinner"></div>
  <h1 style="font-size:20px;margin:0 0 8px">登录成功，正在进入系统...</h1>
  <p style="font-size:14px;color:#666;margin:0">企业微信身份已校验通过</p>
</div>
<script>
  localStorage.setItem("__dootask_kpi__auth_token", ` + string(tokenJSON) + `);
  localStorage.setItem("__dootask_kpi__user_info", ` + string(userJSON) + `);
  location.replace("` + wecomLoginSuccessRedirect + `");
</script>
</body>
</html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// wecomRedirectURI 拼接 OAuth 回调地址。
// 优先使用 WECOM_BASE_URL（企微后台配置的可信域名）；未配置时按当前请求推导，
// 便于内网/本机调试场景使用。
func wecomRedirectURI(c *gin.Context, baseURL string) string {
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/") + "/api/auth/wecom/callback"
	}
	if v, ok := c.Get("base_url"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s + "/api/auth/wecom/callback"
		}
	}
	return ""
}

// renderWeComPage 输出简洁可读的企微登录提示页。
func renderWeComPage(c *gin.Context, status int, title, body string) {
	t := html.EscapeString(title)
	b := html.EscapeString(body)

	page := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + t + `</title>
<style>
  body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif;background:#f5f5f5;color:#333;display:flex;align-items:center;justify-content:center;min-height:100vh;padding:16px}
  .box{background:#fff;border-radius:12px;padding:32px 40px;text-align:center;box-shadow:0 4px 24px rgba(0,0,0,.08);max-width:440px}
  .icon{font-size:40px;line-height:1;margin-bottom:12px}
  h1{font-size:20px;margin:0 0 10px}
  p{font-size:14px;color:#555;line-height:1.7;margin:0 0 20px}
  a{display:inline-block;padding:8px 24px;border-radius:8px;background:#2563eb;color:#fff;text-decoration:none;font-size:14px}
</style>
</head>
<body>
<div class="box">
  <div class="icon">ℹ️</div>
  <h1>` + t + `</h1>
  <p>` + b + `</p>
  <a href="/auth/login">返回登录页</a>
</div>
</body>
</html>`
	c.Data(status, "text/html; charset=utf-8", []byte(page))
}

// randomState 生成 16 位十六进制随机串，用于 OAuth state 防 CSRF。
func randomState() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
