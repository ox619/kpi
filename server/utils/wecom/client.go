// Package wecom 封装企业微信自建应用的接入能力：OAuth 免登、应用消息推送、
// 通讯录读取与变更回调。
//
// 设计约束：
//
//  1. 业务层只依赖本包，不直接 import go-workwx。将来升级到 go-workwx v2
//     （目前仍是 alpha-only）或更换 SDK 时，只需改这一层。
//  2. 所有能力在配置缺失时必须可降级：Enabled() 为 false 时调用方跳过推送，
//     不影响考核流程。
//  3. 通讯录的最终数据源是 ListDirectory 的全量结果，不是回调事件。
//     go-workwx v1.7.0 的事件模型不包含 delete_user 与部门变更，详见 callback.go。
package wecom

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	workwx "github.com/xen0n/go-workwx"
)

// ErrDisabled 表示企业微信未配置，调用方应降级处理。
var ErrDisabled = errors.New("wecom: 未配置企业微信（缺少 corp_id / agent_id / secret）")

// Client 企业微信客户端。内部持有 SDK 实例，access_token 由 SDK 缓存并后台刷新，
// 因此整个进程只应创建一次。
type Client struct {
	cfg Config
	app *workwx.WorkwxApp
}

// httpTimeout 单次企业微信 API 调用的超时。
// SDK 默认使用无超时的 http.Client，若不设置，消息推送可能把业务请求挂住。
const httpTimeout = 10 * time.Second

// New 创建客户端并启动 access_token 后台刷新。
// 重复创建会启动多个刷新 goroutine，请配合下面的 InitFromEnv 单例使用。
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	app := workwx.New(
		cfg.CorpID,
		workwx.WithHTTPClient(&http.Client{Timeout: httpTimeout}),
	).WithApp(cfg.Secret, cfg.AgentID)
	app.SpawnAccessTokenRefresher()

	return &Client{cfg: cfg, app: app}, nil
}

// Config 返回当前配置（Secret 已脱敏，仅用于日志）。
func (c *Client) Config() Config {
	return c.cfg
}

// ---------------------------------------------------------------------------
// 进程级单例
// ---------------------------------------------------------------------------

var (
	globalMu sync.RWMutex
	global   *Client
)

// InitFromEnv 按环境变量初始化全局客户端。
// 未配置时返回 (nil, nil)，表示功能关闭而非错误；配置不完整时返回 error。
func InitFromEnv() (*Client, error) {
	cfg := ConfigFromEnv()
	if !cfg.Enabled() {
		return nil, nil
	}

	client, err := New(cfg)
	if err != nil {
		return nil, err
	}

	globalMu.Lock()
	global = client
	globalMu.Unlock()

	return client, nil
}

// Current 返回全局客户端，未初始化时为 nil。
func Current() *Client {
	globalMu.RLock()
	defer globalMu.RUnlock()

	return global
}

// Notify 使用全局客户端推送消息；未配置时静默跳过。
// 业务代码可以直接调用，不需要自己判空。
func Notify(userID string, m Message) error {
	c := Current()
	if c == nil {
		return nil
	}

	return c.Notify(userID, m)
}

// ---------------------------------------------------------------------------
// 消息推送
// ---------------------------------------------------------------------------

// 企业微信 textcard 消息的字段长度上限（字节，不是字符）。
const (
	maxCardTitleBytes = 128
	maxCardBodyBytes  = 512
)

// Message 一条应用消息。URL 非空时发 textcard（可点击跳转），
// 为空时退化为纯文本。
type Message struct {
	// Title 卡片标题，超过 128 字节会被截断。
	Title string
	// Body 卡片描述，超过 512 字节会被截断。
	Body string
	// URL 点击卡片后跳转的地址，必须是可信域名下的完整 URL。
	URL string
	// ButtonText 按钮文案，默认「查看详情」。
	ButtonText string
}

// Notify 向单个成员推送应用消息。
//
// 注意：userID 是企业微信的 UserID，不是 DooTask 的用户 ID，
// 也不是手机号。调用方需要先通过员工映射关系拿到它。
func (c *Client) Notify(userID string, m Message) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("wecom: userID 为空")
	}

	recipient := &workwx.Recipient{UserIDs: []string{userID}}

	if m.URL == "" {
		body := truncateBytes(m.Body, maxCardBodyBytes)

		return c.app.SendTextMessage(recipient, body, false)
	}

	title := truncateBytes(m.Title, maxCardTitleBytes)
	if title == "" {
		title = "绩效考核"
	}
	body := truncateBytes(m.Body, maxCardBodyBytes)
	buttonText := m.ButtonText
	if buttonText == "" {
		buttonText = "查看详情"
	}

	if err := c.app.SendTextCardMessage(recipient, title, body, m.URL, buttonText, false); err != nil {
		return fmt.Errorf("wecom: 发送卡片消息失败: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// OAuth 免登
// ---------------------------------------------------------------------------

const oauthAuthorizeURL = "https://open.weixin.qq.com/connect/oauth2/authorize"

// BuildOAuthURL 拼接企业微信网页授权链接（静默授权 snsapi_base）。
// redirectURI 必须是应用可信域名下的地址，否则企业微信会报 redirect_uri 错误。
func BuildOAuthURL(corpID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("appid", corpID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "snsapi_base")
	q.Set("state", state)

	return oauthAuthorizeURL + "?" + q.Encode() + "#wechat_redirect"
}

// OAuthURL 使用当前配置拼接授权链接。
func (c *Client) OAuthURL(redirectURI, state string) string {
	return BuildOAuthURL(c.cfg.CorpID, redirectURI, state)
}

// UserIDByCode 用网页授权拿到的 code 换取成员 UserID。
// code 只能使用一次，5 分钟内有效。
func (c *Client) UserIDByCode(code string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", errors.New("wecom: code 为空")
	}

	info, err := c.app.AuthCode2UserInfo(code)
	if err != nil {
		return "", fmt.Errorf("wecom: 用 code 换取 userid 失败: %w", err)
	}
	if info.UserID == "" {
		// 非本企业成员（例如外部联系人）时 userid 为空。
		return "", errors.New("wecom: 该身份不属于本企业成员")
	}

	return info.UserID, nil
}

// ---------------------------------------------------------------------------
// 通讯录
// ---------------------------------------------------------------------------

// User 通讯录成员。
type User struct {
	UserID   string
	Name     string
	Email    string
	Mobile   string
	Position string
	// DeptIDs 所属部门 ID 列表。
	DeptIDs []int64
	// LeaderDeptIDs 该成员担任负责人的部门 ID 列表（企业微信 is_leader_in_dept）。
	// 同步时用它推断「谁是谁的上级」：某个部门唯一的负责人，即该部门其他成员的上级。
	LeaderDeptIDs []int64
	// IsLeader 是否在任一所属部门内为上级。
	IsLeader bool
	// IsEnabled 是否已启用（false 通常意味着已停用/离职）。
	IsEnabled bool
	// Status 企业微信的激活状态原始值。
	Status int
}

// Dept 部门。
type Dept struct {
	ID       int64
	Name     string
	ParentID int64
	Order    uint32
}

// rootDeptID 企业微信根部门 ID 固定为 1。
const rootDeptID = 1

// GetUser 读取单个成员详情。
func (c *Client) GetUser(userID string) (*User, error) {
	info, err := c.app.GetUser(userID)
	if err != nil {
		return nil, fmt.Errorf("wecom: 读取成员 %s 失败: %w", userID, err)
	}

	u := convertUser(info)

	return &u, nil
}

// ListDepts 拉取完整部门树。一次 API 调用，可用于校验凭证是否有效。
func (c *Client) ListDepts() ([]Dept, error) {
	deptInfos, err := c.app.ListAllDepts()
	if err != nil {
		return nil, fmt.Errorf("wecom: 拉取部门列表失败: %w", err)
	}

	depts := make([]Dept, 0, len(deptInfos))
	for _, d := range deptInfos {
		if d == nil {
			continue
		}
		depts = append(depts, Dept{
			ID:       d.ID,
			Name:     d.Name,
			ParentID: d.ParentID,
			Order:    d.Order,
		})
	}

	return depts, nil
}

// ListDirectory 全量拉取通讯录，用于定时对账。
// 只用两次 API 调用：根部门下的全部成员 + 完整部门树。
func (c *Client) ListDirectory() ([]User, []Dept, error) {
	infos, err := c.app.ListUsersByDeptID(rootDeptID, true)
	if err != nil {
		return nil, nil, fmt.Errorf("wecom: 拉取成员列表失败: %w", err)
	}

	depts, err := c.ListDepts()
	if err != nil {
		return nil, nil, err
	}

	users := make([]User, 0, len(infos))
	seen := make(map[string]struct{}, len(infos))
	for _, info := range infos {
		if info == nil {
			continue
		}
		// fetch_child=true 理论上不会重复，保险起见按 UserID 去重。
		if _, ok := seen[info.UserID]; ok {
			continue
		}
		seen[info.UserID] = struct{}{}
		users = append(users, convertUser(info))
	}

	return users, depts, nil
}

func convertUser(info *workwx.UserInfo) User {
	u := User{
		UserID:    info.UserID,
		Name:      info.Name,
		Email:     info.Email,
		Mobile:    info.Mobile,
		Position:  info.Position,
		IsEnabled: info.IsEnabled,
		Status:    int(info.Status),
	}

	for _, d := range info.Departments {
		u.DeptIDs = append(u.DeptIDs, d.DeptID)
		if d.IsLeader {
			u.IsLeader = true
			u.LeaderDeptIDs = append(u.LeaderDeptIDs, d.DeptID)
		}
	}

	return u
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

// truncateBytes 按字节上限截断字符串，且不会把 UTF-8 字符截成半个。
// 企业微信的 textcard 限制是按字节算的，中文一个字 3 字节，很容易超。
func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}

	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}

	return s[:max]
}
