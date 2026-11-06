package handlers

import (
	"fmt"
	"log"
	"strings"

	"dootask-kpi-server/models"
	"dootask-kpi-server/utils/wecom"

	"github.com/gin-gonic/gin"
)

// 考核流程的企业微信推送。
//
// 推送范围遵循「下一步由谁操作就推给谁」：
//   - 创建考核 / 自评完成      → 直属上级（去评分）
//   - 主管评分完成             → 全部 HR（去审核）
//   - HR 审核完成              → 被考核员工（去确认）
//   - 员工确认完成             → 全部 HR（存档知情）
//   - 员工提交异议             → 全部 HR + 直属上级
//   - HR 处理异议              → 被考核员工 + 直属上级
//
// 所有推送在企微未配置、员工未绑定、或跳转地址缺失时静默跳过；
// 发送失败只记日志，不影响接口返回，也不影响 DooTask 机器人通知。

// wecomPushEvaluation 向单个员工的企业微信推送一条考核待办。
// emp 为 nil 或未绑定企业微信时直接跳过。
func wecomPushEvaluation(c *gin.Context, emp *models.Employee, title, body string, evaluationID uint) {
	if emp == nil || emp.WeComUserID == nil {
		return
	}

	userID := strings.TrimSpace(*emp.WeComUserID)
	if userID == "" {
		return
	}

	// 跳转地址必须在请求协程内算好：异步协程里不能再碰 gin.Context。
	url := wecomEvaluationURL(c, evaluationID)

	// 只有真的发出了链接才需要兜底提示，纯文本消息没有「打不开」的问题。
	if url != "" {
		body = appendWorkbenchHint(body)
	}

	// 异步发送：企业微信接口单次超时 10s，不能拖慢考核接口的响应。
	// 启动 goroutine 前把要用的值取出来，避免并发读写。
	employeeID, employeeName := emp.ID, emp.Name
	go func() {
		// 异步协程里的 panic 会带崩整个进程，这里兜住并只记日志。
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[wecom] 推送协程异常：员工 id=%d name=%s err=%v", employeeID, employeeName, r)
			}
		}()

		err := wecom.Notify(userID, wecom.Message{
			Title:      title,
			Body:       body,
			URL:        url,
			ButtonText: "去处理",
		})
		if err != nil {
			log.Printf("[wecom] 推送考核消息失败：员工 id=%d name=%s err=%v", employeeID, employeeName, err)
		}
	}()
}

// wecomPushEvaluationToHRs 向所有已绑定企业微信的 HR 推送。
func wecomPushEvaluationToHRs(c *gin.Context, title, body string, evaluationID uint) {
	hrUserIDs := GetNotificationService().GetAllHRUsers()
	if len(hrUserIDs) == 0 {
		return
	}

	var hrs []models.Employee
	if err := models.DB.Where("id IN ?", hrUserIDs).Find(&hrs).Error; err != nil {
		log.Printf("[wecom] 查询 HR 列表失败，跳过企微推送：%v", err)
		return
	}

	for i := range hrs {
		wecomPushEvaluation(c, &hrs[i], title, body, evaluationID)
	}
}

// wecomEvaluationURL 拼考核详情的跳转地址，指向前端考核页并由 evaluation_id
// 直接打开对应记录。
//
// 基地址与 OAuth 回调复用同一套规则（见 wecomFrontendBase）：优先 WECOM_BASE_URL
// 这一前端对外访问地址（含 basePath），未配置时按当前请求推导。为空则返回空串，
// Notify 会自动退化为纯文本消息。
func wecomEvaluationURL(c *gin.Context, evaluationID uint) string {
	var base string
	if client := wecom.Current(); client != nil {
		base = wecomFrontendBase(c, client.Config().BaseURL)
	}

	return buildEvaluationURL(base, evaluationID)
}

// wecomWorkbenchHint 直链的兜底提示。
//
// 直链本身不含任何凭证，打开的是前端考核页；但企业微信内置浏览器里若登录态
// （Cookie）已失效或用户已退出，直链会落到登录页而无从下手。从工作台的应用
// 入口进入可以顺带拿到完整的企微登录态，是更稳的一条路，因此两条路都给出。
const wecomWorkbenchHint = "如无法打开，请从企业微信工作台进入 绩效管理 应用查看"

// appendWorkbenchHint 在正文末尾追加工作台兜底提示。
//
// 正文过长时先截断正文本身，保证这条提示不会被 textcard 的正文上限截掉。
func appendWorkbenchHint(body string) string {
	suffix := "\n" + wecomWorkbenchHint

	limit := wecom.MaxCardBodyBytes - len(suffix)
	if limit <= 0 {
		return wecomWorkbenchHint
	}

	return truncateText(body, limit) + suffix
}

// truncateText 按字节上限从尾部截断，且不切断 UTF-8 字符。
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}

	cut := 0
	for i := range s {
		if i > max {
			break
		}
		cut = i
	}

	return s[:cut]
}

// buildEvaluationURL 由前端基地址拼出考核详情链接。
// 基地址为空时返回空串，调用方应退化为不带链接的消息。
func buildEvaluationURL(base string, evaluationID uint) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}

	return fmt.Sprintf("%s/evaluations?evaluation_id=%d", base, evaluationID)
}
