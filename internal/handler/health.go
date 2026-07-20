// Package handler - 健康检查 (含依赖探活)。
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/pkg/database"
)

// HealthCheck 返回服务及其依赖 (PostgreSQL / Redis) 的健康状态。
// GET /health
//
// 全部正常返回 200；任一依赖异常返回 503，便于负载均衡与 K8s 探针剔除坏节点。
func HealthCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dbOK := pingDB(ctx)
	redisOK := pingRedis(ctx)

	status := "ok"
	code := http.StatusOK
	if !dbOK || !redisOK {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	c.JSON(code, gin.H{
		"status":  status,
		"service": "zaima-backend",
		"deps": gin.H{
			"postgres": dbOK,
			"redis":    redisOK,
		},
	})
}

func pingDB(ctx context.Context) bool {
	if database.DB == nil {
		return false
	}
	sqlDB, err := database.DB.DB()
	if err != nil {
		return false
	}
	return sqlDB.PingContext(ctx) == nil
}

func pingRedis(ctx context.Context) bool {
	if database.RDB == nil {
		return false
	}
	return database.RDB.Ping(ctx).Err() == nil
}
