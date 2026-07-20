// Package scheduler 提供后台定时任务：气泡清理、月报生成、新闻抓取。
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
)

// CleanExpiredBubbles 将已过期的广场气泡标记为消失，并清理 Redis GEO 数据。
func CleanExpiredBubbles(ctx context.Context) {
	now := time.Now()
	var expired []model.SquareBubble
	if err := database.DB.Where("status = 1 AND expire_at <= ?", now).Find(&expired).Error; err != nil {
		log.Printf("[scheduler] 查询过期气泡失败: %v", err)
		return
	}
	if len(expired) == 0 {
		return
	}
	ids := make([]uint64, 0, len(expired))
	for _, b := range expired {
		ids = append(ids, b.ID)
		member := fmt.Sprintf("bubble:%d", b.ID)
		database.RDB.ZRem(ctx, "square:geo", member)
		database.RDB.Del(ctx, fmt.Sprintf("square:ttl:%d", b.ID))
	}
	database.DB.Model(&model.SquareBubble{}).Where("id IN ?", ids).Update("status", 0)
	log.Printf("[scheduler] 已清理 %d 个过期气泡", len(ids))
}

// monthlyInsight 月度报告的聚合数据结构。
type monthlyInsight struct {
	Month         string `json:"month"`
	ActiveDays    int    `json:"active_days"`
	AvgSteps      int    `json:"avg_steps"`
	TotalSteps    int    `json:"total_steps"`
	AvgScreenMins int    `json:"avg_screen_mins"`
	MinBattery    int    `json:"min_battery"`
	Summary       string `json:"summary"`
}

// GenerateMonthlyReports 为上个月缺失报告的老人生成月度报告 (幂等)。
func GenerateMonthlyReports(ctx context.Context) {
	month := previousMonth(time.Now())

	var elders []model.User
	database.DB.Where("role = 1").Find(&elders)

	for _, elder := range elders {
		// 已有报告则跳过
		var exist int64
		database.DB.Model(&model.MonthlyReport{}).
			Where("user_id = ? AND report_month = ?", elder.ID, month).Count(&exist)
		if exist > 0 {
			continue
		}

		var logs []model.DeviceStatusLog
		database.DB.Where("user_id = ? AND record_date LIKE ?", elder.ID, month+"%").Find(&logs)
		if len(logs) == 0 {
			continue // 当月无数据不生成
		}

		insight := aggregate(month, logs)
		data, _ := json.Marshal(insight)
		report := model.MonthlyReport{
			UserID:      elder.ID,
			ReportMonth: month,
			InsightData: string(data),
		}
		if err := database.DB.Create(&report).Error; err != nil {
			log.Printf("[scheduler] 生成月报失败 user=%d: %v", elder.ID, err)
			continue
		}
	}
	log.Printf("[scheduler] 月报生成检查完成: month=%s", month)
}

func aggregate(month string, logs []model.DeviceStatusLog) monthlyInsight {
	var totalSteps, totalScreen, minBattery int
	minBattery = 100
	for _, l := range logs {
		totalSteps += l.Steps
		totalScreen += l.ScreenUsageMins
		if l.BatteryLevel < minBattery {
			minBattery = l.BatteryLevel
		}
	}
	days := len(logs)
	avgSteps := totalSteps / days
	avgScreen := totalScreen / days

	summary := fmt.Sprintf("本月长辈活跃 %d 天，日均步数 %d 步，日均使用手机 %d 分钟。",
		days, avgSteps, avgScreen)
	if avgSteps < 2000 {
		summary += "活动量偏少，建议多陪长辈户外走动。"
	} else {
		summary += "整体活动规律，状态不错。"
	}

	return monthlyInsight{
		Month:         month,
		ActiveDays:    days,
		AvgSteps:      avgSteps,
		TotalSteps:    totalSteps,
		AvgScreenMins: avgScreen,
		MinBattery:    minBattery,
		Summary:       summary,
	}
}

// previousMonth 返回给定时间的上一个自然月, 格式 2006-01。
func previousMonth(t time.Time) string {
	firstOfThisMonth := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	prev := firstOfThisMonth.AddDate(0, 0, -1)
	return prev.Format("2006-01")
}
