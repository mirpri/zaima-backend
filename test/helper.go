// Package test 提供所有测试公用的初始化辅助函数。
//
// 核心能力:
//   - SetupTestDB:     使用 SQLite 内存数据库模拟 PostgreSQL，自动建表。
//   - SetupTestRedis:  使用 miniredis 启动一个纯 Go 实现的内存 Redis。
//   - SetupTestRouter: 构建一个 Gin 测试引擎，可直接用 httptest 发起请求。
//   - GetTestToken:    快捷生成合法 JWT，方便鉴权接口联调。
package test

import (
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"zaima-backend/internal/config"
	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/utils"
	"zaima-backend/internal/router"
	"zaima-backend/internal/ws"
)

const (
	TestSecret = "test-jwt-secret"
	TestPhone  = "13800138000"
)

// SetupTestDB 初始化 SQLite 内存数据库并自动建表。
// 注意: SQLite 不支持 JSONB 等 PG 特性，测试时仅验证逻辑正确性。
func SetupTestDB() *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		panic("测试数据库初始化失败: " + err.Error())
	}

	// 自动建表
	_ = db.AutoMigrate(
		&model.User{},
		&model.UserRelation{},
		&model.UserInterest{},
		&model.Friendship{},
		&model.DeviceStatusLog{},
		&model.MonthlyReport{},
		&model.ChatMessage{},
		&model.SquareBubble{},
		&model.NewsCache{},
	)

	// 赋值全局变量供 handler 使用
	database.DB = db
	return db
}

// SetupTestRedis 使用 miniredis 启动纯内存 Redis 实例 (无需真实 Redis 进程)。
func SetupTestRedis() *miniredis.Miniredis {
	mr, err := miniredis.Run()
	if err != nil {
		panic("miniredis 启动失败: " + err.Error())
	}

	database.RDB = redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return mr
}

// SetupTestConfig 初始化测试用的全局配置。
func SetupTestConfig() {
	config.AppConfig = &config.Config{
		Server: config.ServerConfig{
			Port: 8080,
			Mode: "test",
		},
		JWT: config.JWTConfig{
			Secret:      TestSecret,
			ExpireHours: 24,
		},
		Storage: config.StorageConfig{
			PublicBaseURL: "https://cdn.zaima.test",
			MaxSizeMB:     10,
		},
		Weather: config.WeatherConfig{
			CacheTTLHrs: 1,
		},
		News: config.NewsConfig{
			CacheTTLHrs: 1,
		},
	}
}

// SetupTestRouter 一键构建可发 httptest 请求的 Gin 引擎。
// 每次调用会清空所有表数据，确保测试隔离。
func SetupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	SetupTestConfig()
	SetupTestDB()
	SetupTestRedis()

	// 清空所有表数据，确保测试隔离
	CleanDB()

	hub := ws.NewHub()
	go hub.Run()

	return router.SetupRouter(hub)
}

// 清空所有表数据
func CleanDB() {
	database.DB.Exec("DELETE FROM users")
	database.DB.Exec("DELETE FROM user_relations")
	database.DB.Exec("DELETE FROM user_interests")
	database.DB.Exec("DELETE FROM friendships")
	database.DB.Exec("DELETE FROM device_status_logs")
	database.DB.Exec("DELETE FROM monthly_reports")
	database.DB.Exec("DELETE FROM chat_messages")
	database.DB.Exec("DELETE FROM square_bubbles")
	database.DB.Exec("DELETE FROM news_cache")
}

// GetTestToken 生成一个测试用 JWT Token。
func GetTestToken(userID uint64, role int) string {
	token, _ := utils.GenerateToken(userID, TestPhone, role, TestSecret, 24)
	return token
}

// SeedElderUser 在测试库中预置一个老人用户并返回 ID。
func SeedElderUser(db *gorm.DB) uint64 {
	user := model.User{
		Phone:    "13900000001",
		Role:     1,
		Nickname: "张大爷",
		City:     "武汉",
		Province: "湖北",
	}
	db.Create(&user)
	return user.ID
}

// SeedYouthUser 在测试库中预置一个年轻人用户并返回 ID。
func SeedYouthUser(db *gorm.DB) uint64 {
	user := model.User{
		Phone:    "13900000002",
		Role:     2,
		Nickname: "小明",
		City:     "深圳",
		Province: "广东",
	}
	db.Create(&user)
	return user.ID
}
