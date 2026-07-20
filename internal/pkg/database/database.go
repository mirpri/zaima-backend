// Package database 提供 PostgreSQL 与 Redis 的连接初始化。
package database

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"zaima-backend/internal/config"
	"zaima-backend/internal/model"
)

// DB 全局数据库连接实例。
var DB *gorm.DB

// RDB 全局 Redis 客户端实例。
var RDB *redis.Client

// InitPostgres 初始化 PostgreSQL 连接并执行自动建表。
func InitPostgres(cfg *config.DatabaseConfig) {
	var err error
	DB, err = gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		log.Fatalf("[database] PostgreSQL 连接失败: %v", err)
	}

	sqlDB, _ := DB.DB()
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)

	// 自动迁移所有模型 (创建/更新表结构)
	if err = DB.AutoMigrate(
		&model.User{},
		&model.UserRelation{},
		&model.UserInterest{},
		&model.Friendship{},
		&model.DeviceStatusLog{},
		&model.MonthlyReport{},
		&model.ChatMessage{},
		&model.SquareBubble{},
		&model.NewsCache{},
	); err != nil {
		log.Fatalf("[database] AutoMigrate 失败: %v", err)
	}

	log.Println("[database] PostgreSQL 连接成功，表结构已同步")
}

// InitRedis 初始化 Redis 连接。
func InitRedis(cfg *config.RedisConfig) {
	RDB = redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if _, err := RDB.Ping(context.Background()).Result(); err != nil {
		log.Fatalf("[database] Redis 连接失败: %v", err)
	}
	log.Println("[database] Redis 连接成功")
}
