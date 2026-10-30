package wecom

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	workwx "github.com/xen0n/go-workwx"
)

// DirectoryEventType 通讯录变更事件类型。
type DirectoryEventType string

const (
	// EventUserCreated 新成员加入。
	EventUserCreated DirectoryEventType = "user_created"
	// EventUserUpdated 成员信息变更。
	EventUserUpdated DirectoryEventType = "user_updated"
)

// DirectoryEvent 通讯录变更事件，已转换成不依赖 SDK 的结构。
type DirectoryEvent struct {
	Type      DirectoryEventType
	UserID    string
	NewUserID string
	Name      string
	Email     string
	Mobile    string
	Position  string
	// DeptIDs 成员所属部门 ID 列表。
	DeptIDs []int64
	// IsLeaderInDept 与 DeptIDs 一一对应，表示是否在该部门内为上级。
	IsLeaderInDept []bool
	// Status 企业微信激活状态原始值。
	Status int
}

// EventHandler 处理通讯录变更事件。回调在 HTTP 请求 goroutine 中执行，
// 必须尽快返回，重活请自行丢到队列里。
type EventHandler func(DirectoryEvent)

// CallbackHandler 返回可直接挂到 Gin 路由上的 http.Handler。
//
// 它负责企业微信回调所需的全部协议细节：URL 验证（echostr 回显）、
// msg_signature 验签、AES-256-CBC 解密、PKCS7 去填充，这些都由
// go-workwx 的 lowlevel 实现完成，不需要我们自己写加解密。
//
// 已知限制（go-workwx v1.7.0 与 v2 均如此）：
//
//	change_contact 事件只解析 create_user / update_user，
//	delete_user 以及 create_party / update_party / delete_party
//	会走到 default 分支返回 error，handler 以 5xx 响应，
//	企业微信随后重试若干次。
//
// 因此成员离职、部门调整必须以 ListDirectory 的定时全量对账为准，
// 本回调只用来降低「新人入职到可登录」的延迟，不能当作唯一数据源。
func (c *Client) CallbackHandler(onEvent EventHandler) (http.Handler, error) {
	if !c.cfg.CallbackEnabled() {
		return nil, errors.New(
			"wecom: 启用通讯录回调需要同时配置 " + EnvCallbackToken + " 与 " + EnvCallbackAESKey,
		)
	}

	handler, err := workwx.NewHTTPHandler(
		c.cfg.CallbackToken,
		c.cfg.CallbackAESKey,
		&callbackDispatcher{onEvent: onEvent},
	)
	if err != nil {
		return nil, err
	}

	return handler, nil
}

type callbackDispatcher struct {
	onEvent EventHandler
}

var _ workwx.RxMessageHandler = (*callbackDispatcher)(nil)

func (d *callbackDispatcher) OnIncomingMessage(msg *workwx.RxMessage) error {
	if msg == nil {
		return errors.New("wecom: 收到空事件")
	}
	if d.onEvent == nil {
		return nil
	}

	// SDK 把 create_user 与 update_user 定义成两个不同的未导出结构体，
	// 无法写成公共函数的入参类型，只能在此分别取值。
	// 未导出类型的导出字段是可以直接访问的。
	if u, ok := msg.EventChangeTypeCreateUser(); ok {
		d.onEvent(DirectoryEvent{
			Type:           EventUserCreated,
			UserID:         u.UserID,
			Name:           u.Name,
			Email:          u.Email,
			Mobile:         u.Mobile,
			Position:       u.Position,
			DeptIDs:        splitInt64List(u.Department),
			IsLeaderInDept: splitBoolList(u.IsLeaderInDept),
			Status:         u.Status,
		})

		return nil
	}

	if u, ok := msg.EventChangeTypeUpdateUser(); ok {
		d.onEvent(DirectoryEvent{
			Type:           EventUserUpdated,
			UserID:         u.UserID,
			NewUserID:      u.NewUserID,
			Name:           u.Name,
			Email:          u.Email,
			Mobile:         u.Mobile,
			Position:       u.Position,
			DeptIDs:        splitInt64List(u.Department),
			IsLeaderInDept: splitBoolList(u.IsLeaderInDept),
			Status:         u.Status,
		})

		return nil
	}

	return nil
}

// splitInt64List 解析企业微信的竖线分隔列表，例如 "1|2|3"。
func splitInt64List(s string) []int64 {
	parts := splitPipeList(s)
	if len(parts) == 0 {
		return nil
	}

	values := make([]int64, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			continue
		}
		values = append(values, v)
	}

	return values
}

// splitBoolList 解析企业微信的竖线分隔 0/1 列表，例如 "1|0"。
func splitBoolList(s string) []bool {
	parts := splitPipeList(s)
	if len(parts) == 0 {
		return nil
	}

	values := make([]bool, 0, len(parts))
	for _, p := range parts {
		values = append(values, p == "1")
	}

	return values
}

func splitPipeList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	parts := strings.Split(s, "|")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}

	return out
}
