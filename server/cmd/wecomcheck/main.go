// 企业微信连通性自检工具。
//
// 它不参与服务运行，只在首次配置或线上排障时手工执行，用来确认
// corp_id / agent_id / secret 是否有效、消息能不能收到、通讯录权限是否
// 已开通。命令行里没有写任何业务逻辑。
//
// 用法（必须在 server/ 目录下，否则读不到同名环境变量约定）：
//
//	cd server
//
//	# 1. 只校验凭证（拉一次部门树）
//	go run ./cmd/wecomcheck
//
//	# 2. 给自己发一条测试消息
//	go run ./cmd/wecomcheck -to zhangsan -msg "KPI 测试"
//
//	# 3. 拉取通讯录，看权限和数据是否正常
//	go run ./cmd/wecomcheck -directory
//
//	# 4. 打印 OAuth 授权链接，浏览器打开可直接验证可信域名配置
//	go run ./cmd/wecomcheck -oauth https://kpi.example.com/api/auth/wecom/callback
package main

import (
	"flag"
	"fmt"
	"os"

	"dootask-kpi-server/utils/wecom"
)

func main() {
	to := flag.String("to", "", "接收测试消息的企业微信 UserID")
	msg := flag.String("msg", "KPI 企业微信连通性测试", "测试消息内容")
	directory := flag.Bool("directory", false, "拉取通讯录并打印统计")
	oauth := flag.String("oauth", "", "打印以该地址为回调的 OAuth 授权链接")
	state := flag.String("state", "wecomcheck", "OAuth state 参数")
	limit := flag.Int("limit", 10, "-directory 时最多打印多少条明细")
	flag.Parse()

	cfg := wecom.ConfigFromEnv()
	fmt.Println("配置:", cfg)

	if !cfg.Enabled() {
		fmt.Fprintf(
			os.Stderr,
			"\n缺少必填环境变量，需要设置 %s / %s / %s，说明见 utils/wecom/config.go\n",
			wecom.EnvCorpID,
			wecom.EnvAgentID,
			wecom.EnvSecret,
		)
		os.Exit(2)
	}

	if *oauth != "" {
		fmt.Println("\nOAuth 授权链接（需在浏览器/企业微信内打开）:")
		fmt.Println(wecom.BuildOAuthURL(cfg.CorpID, *oauth, *state))

		return
	}

	client, err := wecom.New(cfg)
	if err != nil {
		fail("初始化客户端失败", err)
	}

	if *to != "" {
		if err := client.Notify(*to, wecom.Message{Body: *msg}); err != nil {
			fail("发送测试消息失败", err)
		}
		fmt.Printf("\n✅ 已向 %s 发送测试消息\n", *to)
	}

	if *directory {
		users, depts, err := client.ListDirectory()
		if err != nil {
			fail("拉取通讯录失败", err)
		}

		fmt.Printf("\n✅ 通讯录：%d 个部门，%d 个成员\n", len(depts), len(users))

		for i, d := range depts {
			if i >= *limit {
				fmt.Printf("  ...（共 %d 个部门）\n", len(depts))
				break
			}
			fmt.Printf("  [部门] id=%d parent=%d name=%s\n", d.ID, d.ParentID, d.Name)
		}

		for i, u := range users {
			if i >= *limit {
				fmt.Printf("  ...（共 %d 个成员）\n", len(users))
				break
			}
			fmt.Printf(
				"  [成员] %s(%s) dept=%v leader=%v leaderDept=%v enabled=%v email=%q\n",
				u.Name,
				u.UserID,
				u.DeptIDs,
				u.IsLeader,
				u.LeaderDeptIDs,
				u.IsEnabled,
				u.Email,
			)
		}

		return
	}

	if *to == "" {
		// 没有指定接收人时，至少校验一次凭证是否可用。
		depts, err := client.ListDepts()
		if err != nil {
			fail("凭证校验失败", err)
		}
		fmt.Printf("\n✅ 凭证有效，可见 %d 个部门\n", len(depts))
	}
}

func fail(action string, err error) {
	fmt.Fprintf(os.Stderr, "\n❌ %s: %v\n", action, err)
	os.Exit(1)
}
