// Package scheduler - 基于 time.Ticker 的轻量定时任务调度器 (无第三方依赖)。
package scheduler

import (
	"context"
	"log"
	"time"

	"zaima-backend/internal/config"
	"zaima-backend/internal/pkg/news"
)

// Start 启动所有后台定时任务，随 ctx 取消而优雅退出。
func Start(ctx context.Context) {
	// 气泡清理: 每 10 分钟
	go runEvery(ctx, 10*time.Minute, false, func() {
		CleanExpiredBubbles(ctx)
	})

	// 月报生成检查: 每 12 小时 (幂等, 只补缺失)
	go runEvery(ctx, 12*time.Hour, false, func() {
		GenerateMonthlyReports(ctx)
	})

	// 新闻抓取: 启动时先抓一次, 之后按配置周期 (默认 6 小时)
	newsInterval := time.Duration(config.AppConfig.News.CacheTTLHrs) * time.Hour
	if newsInterval <= 0 {
		newsInterval = 6 * time.Hour
	}
	go runEvery(ctx, newsInterval, true, func() {
		news.FetchAll(ctx)
	})

	log.Println("[scheduler] 后台定时任务已启动")
}

// runEvery 每隔 interval 执行 job；runNow 为 true 时先立即执行一次。
func runEvery(ctx context.Context, interval time.Duration, runNow bool, job func()) {
	if runNow {
		safeRun(job)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			safeRun(job)
		}
	}
}

// safeRun 执行 job 并捕获 panic，避免单个任务异常导致 goroutine 退出。
func safeRun(job func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[scheduler] 任务 panic 已恢复: %v", r)
		}
	}()
	job()
}
