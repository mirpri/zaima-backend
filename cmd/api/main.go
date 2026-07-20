// Package main 是 "在吗" APP 后端服务的入口。
//
// 启动流程:
//  1. 加载配置文件 (configs/config.yaml)
//  2. 初始化 PostgreSQL + Redis 连接
//  3. 启动 WebSocket Hub 与后台定时任务 (独立 goroutine)
//  4. 注册路由并启动 HTTP 服务 (支持优雅停机)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/config"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/router"
	"zaima-backend/internal/scheduler"
	"zaima-backend/internal/ws"
)

func main() {
	// 命令行参数：指定配置文件路径
	configPath := flag.String("config", "configs/config.yaml", "配置文件路径")
	flag.Parse()

	// 1. 加载配置
	config.Load(*configPath)

	// 2. 设置 Gin 模式
	gin.SetMode(config.AppConfig.Server.Mode)

	// 3. 初始化数据库连接
	database.InitPostgres(&config.AppConfig.Database)
	database.InitRedis(&config.AppConfig.Redis)

	// 根 context，用于协调后台任务优雅退出
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. 启动 WebSocket Hub 与后台定时任务
	hub := ws.NewHub()
	go hub.Run()
	scheduler.Start(rootCtx)

	// 5. 初始化路由并启动 HTTP 服务
	r := router.SetupRouter(hub)
	addr := fmt.Sprintf(":%d", config.AppConfig.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		log.Printf("[main] 🚀 在吗后端服务启动: http://localhost%s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[main] 服务启动失败: %v", err)
		}
	}()

	// 6. 等待退出信号并优雅关闭
	<-rootCtx.Done()
	stop() // 恢复默认信号处理，二次 Ctrl+C 可强制退出
	log.Println("[main] 正在优雅关闭服务...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[main] 优雅关闭超时: %v", err)
	}
	log.Println("[main] 服务已退出")
}
