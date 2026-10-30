package wecom

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// 企业微信自建应用的配置项全部通过环境变量注入，不写入数据库，
// 避免把 corp secret 落到 SQLite 里。
//
// 企业微信管理后台 → 应用管理 → 自建 → 你的应用：
//
//	WECOM_CORP_ID           企业 ID（我的企业 → 企业信息）
//	WECOM_AGENT_ID          应用的 AgentId
//	WECOM_SECRET            应用的 Secret
//	WECOM_BASE_URL          本系统对外访问地址，例如 https://kpi.example.com
//	                        用于拼 OAuth 回调地址和消息里的跳转链接
//	WECOM_CALLBACK_TOKEN    接收消息服务器配置里的 Token
//	WECOM_CALLBACK_AES_KEY  接收消息服务器配置里的 EncodingAESKey（43 位）
//
// 后两项只在启用通讯录回调时才需要。
const (
	EnvCorpID         = "WECOM_CORP_ID"
	EnvAgentID        = "WECOM_AGENT_ID"
	EnvSecret         = "WECOM_SECRET"
	EnvBaseURL        = "WECOM_BASE_URL"
	EnvCallbackToken  = "WECOM_CALLBACK_TOKEN"
	EnvCallbackAESKey = "WECOM_CALLBACK_AES_KEY"
)

// Config 企业微信自建应用配置。
type Config struct {
	// CorpID 企业 ID。
	CorpID string
	// AgentID 自建应用 AgentId。
	AgentID int64
	// Secret 自建应用 Secret。
	Secret string
	// BaseURL 系统对外访问地址，不带结尾斜杠。
	BaseURL string
	// CallbackToken 通讯录回调的 Token。
	CallbackToken string
	// CallbackAESKey 通讯录回调的 EncodingAESKey，长度必须是 43。
	CallbackAESKey string
}

// ConfigFromEnv 从环境变量读取配置，不做合法性校验。
func ConfigFromEnv() Config {
	agentID, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv(EnvAgentID)), 10, 64)

	return Config{
		CorpID:         strings.TrimSpace(os.Getenv(EnvCorpID)),
		AgentID:        agentID,
		Secret:         strings.TrimSpace(os.Getenv(EnvSecret)),
		BaseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv(EnvBaseURL)), "/"),
		CallbackToken:  strings.TrimSpace(os.Getenv(EnvCallbackToken)),
		CallbackAESKey: strings.TrimSpace(os.Getenv(EnvCallbackAESKey)),
	}
}

// Enabled 表示「能发应用消息、能做 OAuth 免登」所需的配置是否齐全。
// 为 false 时调用方必须降级为站内通知，不能报错中断业务。
func (c Config) Enabled() bool {
	return c.CorpID != "" && c.AgentID > 0 && c.Secret != ""
}

// CallbackEnabled 表示通讯录回调所需的配置是否齐全。
func (c Config) CallbackEnabled() bool {
	return c.CallbackToken != "" && len(c.CallbackAESKey) == 43
}

func (c Config) validate() error {
	if c.CorpID == "" {
		return fmt.Errorf("wecom: 缺少 %s", EnvCorpID)
	}
	if c.AgentID <= 0 {
		return fmt.Errorf("wecom: %s 必须是正整数", EnvAgentID)
	}
	if c.Secret == "" {
		return fmt.Errorf("wecom: 缺少 %s", EnvSecret)
	}

	return nil
}

// maskedSecret 用于日志输出，避免把 Secret 打全。
func (c Config) maskedSecret() string {
	if len(c.Secret) <= 6 {
		return "***"
	}

	return c.Secret[:3] + "***" + c.Secret[len(c.Secret)-3:]
}

// String 实现安全的可打印摘要。
func (c Config) String() string {
	return fmt.Sprintf(
		"wecom.Config{CorpID: %q, AgentID: %d, Secret: %s, BaseURL: %q, CallbackEnabled: %v}",
		c.CorpID,
		c.AgentID,
		c.maskedSecret(),
		c.BaseURL,
		c.CallbackEnabled(),
	)
}
