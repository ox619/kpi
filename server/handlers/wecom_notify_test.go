package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"dootask-kpi-server/utils/wecom"
	"github.com/gin-gonic/gin"
)

// 考核推送的跳转链接必须能直接打开前端考核详情页：
// /evaluations 路径 + evaluation_id 查询参数，且不能被 basePath 双斜杠破坏。
func TestBuildEvaluationURL(t *testing.T) {
	cases := []struct {
		name string
		base string
		id   uint
		want string
	}{
		{"未配置基地址", "", 12, ""},
		{"裸域名", "https://kpi.example.com", 12, "https://kpi.example.com/evaluations?evaluation_id=12"},
		{"带尾部斜杠", "https://kpi.example.com/", 12, "https://kpi.example.com/evaluations?evaluation_id=12"},
		{"带 basePath", "https://kpi.example.com/kpi", 7, "https://kpi.example.com/kpi/evaluations?evaluation_id=7"},
		{"带 basePath 和尾部斜杠", "https://kpi.example.com/kpi/", 7, "https://kpi.example.com/kpi/evaluations?evaluation_id=7"},
		{"空白字符", "  https://kpi.example.com/kpi  ", 3, "https://kpi.example.com/kpi/evaluations?evaluation_id=3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildEvaluationURL(tc.base, tc.id); got != tc.want {
				t.Fatalf("buildEvaluationURL(%q, %d) = %q, want %q", tc.base, tc.id, got, tc.want)
			}
		})
	}
}

// 配置了 WECOM_BASE_URL 时应优先使用它，而不是请求里的 host——
// 直连后端端口调试时请求 host 指向 API 端口，跳过去会打不开前端页面。
func TestWeComEvaluationURLPrefersConfiguredBase(t *testing.T) {
	t.Setenv(wecom.EnvCorpID, "corp-test")
	t.Setenv(wecom.EnvAgentID, "100001")
	t.Setenv(wecom.EnvSecret, "secret-test")
	t.Setenv(wecom.EnvBaseURL, "https://kpi.example.com/kpi")

	if _, err := wecom.InitFromEnv(); err != nil {
		t.Fatalf("初始化企业微信配置失败: %v", err)
	}
	t.Cleanup(func() {
		// 复位单例，避免污染同包其它用例
		t.Setenv(wecom.EnvBaseURL, "")
		_, _ = wecom.InitFromEnv()
	})

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "http://10.0.0.5:8000/api/kpi/evaluations", nil)

	want := "https://kpi.example.com/kpi/evaluations?evaluation_id=99"
	if got := wecomEvaluationURL(c, 99); got != want {
		t.Fatalf("wecomEvaluationURL = %q, want %q", got, want)
	}
}

// 推送消息必须在正文末尾带上工作台兜底提示：直链在企微内浏览器里遇到
// 登录态失效会落到登录页，这时需要一条"从工作台进入"的退路。
func TestAppendWorkbenchHint(t *testing.T) {
	got := appendWorkbenchHint("员工：张三\n模板：技术岗位月度考核")
	want := "员工：张三\n模板：技术岗位月度考核\n" + wecomWorkbenchHint

	if got != want {
		t.Fatalf("appendWorkbenchHint = %q, want %q", got, want)
	}
}

// 提示语的内容是既定文案，不允许被随手改动。
func TestWeComWorkbenchHintText(t *testing.T) {
	want := "如无法打开，请从企业微信工作台进入 绩效管理 应用查看"
	if wecomWorkbenchHint != want {
		t.Fatalf("wecomWorkbenchHint = %q, want %q", wecomWorkbenchHint, want)
	}
}

// 正文超长时应先截断正文本身，保证兜底提示完整保留在 512 字节上限之内，
// 而不是被企微的 textcard 上限从末尾截掉。
func TestAppendWorkbenchHintKeepsHintWhenBodyTooLong(t *testing.T) {
	body := strings.Repeat("异议原因：与我理解不一致", 60)

	got := appendWorkbenchHint(body)

	if !strings.HasSuffix(got, "\n"+wecomWorkbenchHint) {
		t.Fatalf("兜底提示被截断，结果末尾为 %q", tail(got, 60))
	}
	if len(got) > wecom.MaxCardBodyBytes {
		t.Fatalf("正文 %d 字节，超过企微上限 %d", len(got), wecom.MaxCardBodyBytes)
	}
}

// 截断不能切出半个 UTF-8 字符（中文 3 字节，奇数上限最容易切坏）。
func TestTruncateTextKeepsUTF8(t *testing.T) {
	cases := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"未超限", "张三", 300, "张三"},
		{"正好等于上限", "abcde", 5, "abcde"},
		{"在字符中间截断", "绩效考核", 7, "绩效"}, // 中 3 字节，7 落在 "考" 中间，只能退到 6
		{"上限小于单个字符", "绩效", 2, ""},
		{"上限为零", "绩效", 0, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateText(tc.input, tc.max)
			if got != tc.want {
				t.Fatalf("truncateText(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.want)
			}
			if len(got) > tc.max {
				t.Fatalf("truncateText(%q, %d) 长度 %d 超限", tc.input, tc.max, len(got))
			}
		})
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
