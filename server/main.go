package main

import (
	"log"
	"net/http"

	"dootask-kpi-server/handlers"
	"dootask-kpi-server/models"
	"dootask-kpi-server/routes"
	"dootask-kpi-server/utils/wecom"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// 初始化数据库
	models.InitDB()

	// 创建测试数据
	models.CreateTestData()

	// 初始化企业微信客户端：未配置 WECOM_* 时静默降级（Current() 为 nil），
	// 不影响现有内网启动与站内登录；配置不完整时仅告警，不阻塞启动。
	if _, err := wecom.InitFromEnv(); err != nil {
		log.Printf("[wecom] 企业微信初始化失败，相关功能降级：%v", err)
	} else if client := wecom.Current(); client != nil {
		log.Printf("[wecom] 企业微信接入已启用：%s", client.Config().String())
	}

	// 设置Gin模式
	gin.SetMode(gin.ReleaseMode)

	// 创建Gin引擎
	r := gin.Default()

	// 配置CORS
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization", "DooTaskAuth"}
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	r.Use(cors.New(config))

	// 基础中间件
	r.Use(handlers.BaseMiddleware())

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "OK",
			"message": "KPI系统后端服务正常运行",
		})
	})

	// 设置路由
	api := r.Group("/api")
	routes.SetupRoutes(api)

	// 启动自动清理任务
	handlers.StartSSECleanupTask()
	handlers.CleanupExportFiles()

	log.Println("KPI系统服务器启动在端口 :8080")
	log.Fatal(r.Run(":8080"))
}
