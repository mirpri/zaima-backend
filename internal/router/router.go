// Package router 定义所有 API 路由及分组。
//
// 路由结构:
//
//	/api/v1/
//	  ├── auth/          认证 (公开)
//	  │   ├── POST sms-code
//	  │   └── POST login
//	  ├── user/          用户 (需鉴权)
//	  │   ├── GET    profile
//	  │   ├── PUT    profile
//	  │   ├── POST   bind
//	  │   ├── POST   bind/confirm
//	  │   └── PUT    interests
//	  ├── device/        设备监控 (需鉴权)
//	  │   ├── POST   upload
//	  │   ├── GET    daily-insight
//	  │   └── GET    monthly-report
//	  ├── square/        广场 (需鉴权)
//	  │   ├── POST   publish
//	  │   ├── GET    bubbles
//	  │   └── POST   match-confirm
//	  ├── weather/       天气 (需鉴权)
//	  │   ├── GET    /
//	  │   └── GET    care-cards
//	  ├── news/          新闻 (需鉴权)
//	  │   └── GET    /
//	  ├── chat/          聊天 REST (需鉴权)
//	  │   ├── GET    sessions
//	  │   ├── GET    history
//	  │   ├── POST   ai-suggest
//	  │   └── POST   stt
//	  └── ws             WebSocket (Token via Query)
package router

import (
	"github.com/gin-gonic/gin"

	"zaima-backend/internal/handler"
	"zaima-backend/internal/middleware"
	"zaima-backend/internal/ws"
)

// SetupRouter 初始化并返回 Gin 引擎，注册所有路由。
func SetupRouter(hub *ws.Hub) *gin.Engine {
	r := gin.Default()

	// 注入实时通知器，供 handler 层在线推送使用
	handler.Notifier = hub

	// 全局中间件
	r.Use(middleware.CORS())

	// 健康检查 (含依赖探活)
	r.GET("/health", handler.HealthCheck)

	// 自建文件存储静态访问: GET /files/*
	r.GET("/files/*filepath", handler.ServeFile)

	// ========== API v1 ==========
	v1 := r.Group("/api/v1")

	// --- 认证模块 (公开接口) ---
	auth := v1.Group("/auth")
	{
		auth.POST("/sms-code", handler.SendSMSCode) // 发送验证码
		auth.POST("/login", handler.Login)          // 登录/注册
	}

	// --- WebSocket (Token 通过 Query 参数传入) ---
	v1.GET("/ws", ws.HandleWebSocket(hub))

	// ========== 以下接口需要 JWT 鉴权 ==========
	authorized := v1.Group("")
	authorized.Use(middleware.JWTAuth())

	// --- 用户模块 ---
	user := authorized.Group("/user")
	{
		user.GET("/profile", handler.GetProfile)        // 获取用户资料
		user.PUT("/profile", handler.UpdateProfile)     // 更新用户资料
		user.GET("/family", handler.GetFamily)          // 已绑定家人列表
		user.POST("/bind", handler.BindRequest)         // 发起亲子绑定
		user.POST("/bind/confirm", handler.BindConfirm) // 确认绑定
		user.PUT("/interests", handler.UpdateInterests) // 更新兴趣标签
	}

	// --- 设备监控模块 ---
	device := authorized.Group("/device")
	{
		device.POST("/upload", handler.UploadDeviceData)        // 上报设备数据
		device.GET("/daily-insight", handler.GetDailyInsight)   // 每日状态+AI建议
		device.GET("/monthly-report", handler.GetMonthlyReport) // 月度AI报告
	}

	// --- 广场模块 ---
	square := authorized.Group("/square")
	{
		square.POST("/publish", handler.PublishBubble)      // 发布气泡
		square.GET("/bubbles", handler.GetBubbles)          // 获取气泡列表
		square.GET("/users", handler.GetSquareUsers)        // 获取广场用户列表
		square.POST("/start-chat", handler.SquareStartChat) // 发起搭子聊天 (建立好友关系)
		square.POST("/match-confirm", handler.MatchConfirm) // 确认匹配
	}

	// --- 天气模块 ---
	weather := authorized.Group("/weather")
	{
		weather.GET("", handler.GetWeather)              // 获取天气
		weather.GET("/care-cards", handler.GetCareCards) // 获取关怀卡片
	}

	// --- 新闻模块 ---
	authorized.GET("/news", handler.GetNews) // 获取新闻列表

	// --- 聊天 REST 模块 ---
	chat := authorized.Group("/chat")
	{
		chat.POST("/create", handler.CreateChat)       // 创建聊天（发起第一次对话）
		chat.POST("/send", handler.SendMessage)        // 发送消息 (REST，落库+在线投递)
		chat.GET("/sessions", handler.GetChatSessions) // 会话列表
		chat.GET("/history", handler.GetChatHistory)   // 聊天历史
		chat.POST("/ai-suggest", handler.AIReply)      // AI回复/开场建议
		chat.GET("/care-suggest", handler.CareSuggest) // 对方天气关怀话语
	}

	// --- 自建文件上传 ---
	authorized.POST("/upload", handler.UploadFile) // 上传头像/录音等媒体文件

	return r
}
