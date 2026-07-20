// Package handler - 设备状态数据上报与 AI 日报/月报。
package handler

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/llm"
	"zaima-backend/internal/pkg/response"
)

// ==================== 请求体定义 ====================

// DeviceUploadReq 老人端每日设备状态上报请求。
type DeviceUploadReq struct {
	RecordDate      string `json:"record_date" binding:"required"` // 格式: 2026-03-05
	Steps           int    `json:"steps"`
	BatteryLevel    int    `json:"battery_level"`
	ScreenUnlocks   int    `json:"screen_unlocks"`
	ScreenUsageMins int    `json:"screen_usage_mins"`
}

// ==================== Handler ====================

// UploadDeviceData 老人端静默上报当日设备状态。
// POST /api/v1/device/upload
func UploadDeviceData(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req DeviceUploadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数不完整")
		return
	}

	// 查找当日是否已有记录 (upsert 逻辑)
	var existing model.DeviceStatusLog
	result := database.DB.Where("user_id = ? AND record_date = ?", userID, req.RecordDate).First(&existing)

	if result.RowsAffected > 0 {
		// 更新已有记录
		database.DB.Model(&existing).Updates(map[string]interface{}{
			"steps":             req.Steps,
			"battery_level":     req.BatteryLevel,
			"screen_unlocks":    req.ScreenUnlocks,
			"screen_usage_mins": req.ScreenUsageMins,
		})
	} else {
		// 插入新记录
		log := model.DeviceStatusLog{
			UserID:          userID,
			RecordDate:      req.RecordDate,
			Steps:           req.Steps,
			BatteryLevel:    req.BatteryLevel,
			ScreenUnlocks:   req.ScreenUnlocks,
			ScreenUsageMins: req.ScreenUsageMins,
		}
		database.DB.Create(&log)
	}

	response.OKWithMsg(c, "数据上报成功", nil)
}

// GetDailyInsight 年轻人端拉取老人当日状态 + AI 建议。
// GET /api/v1/device/daily-insight?elder_id=xxx
//
// 后端组装当日设备数据，投喂 LLM 生成关切建议。
// 安全: 必须校验当前用户与老人存在已确认的亲子绑定关系 (防止 IDOR 越权)。
func GetDailyInsight(c *gin.Context) {
	userID := c.GetUint64("user_id")
	elderIDStr := c.Query("elder_id")
	if elderIDStr == "" {
		response.BadRequest(c, "缺少 elder_id 参数")
		return
	}
	elderID, _ := strconv.ParseUint(elderIDStr, 10, 64)

	// 【IDOR 防护】校验当前用户是否已与该老人绑定
	var relation model.UserRelation
	result := database.DB.Where(
		"status = 1 AND elder_id = ? AND youth_id = ?", elderID, userID,
	).First(&relation)
	if result.RowsAffected == 0 {
		response.Fail(c, 403, "无权查看该长辈的数据")
		return
	}

	// 查询老人最近7天设备状态
	var logs []model.DeviceStatusLog
	database.DB.Where("user_id = ?", elderID).
		Order("record_date DESC").
		Limit(7).
		Find(&logs)

	if len(logs) == 0 {
		response.OK(c, gin.H{
			"status":  "no_data",
			"message": "暂无监控数据，请确认老人是否已开启相关权限",
		})
		return
	}

	today := logs[0]

	// 优先 LLM 生成个性化关切建议，失败/未配置回退规则引擎
	suggestion := generateInsightSuggestion(c.Request.Context(), today, relation.Remark)

	response.OK(c, gin.H{
		"today":      today,
		"history":    logs,
		"suggestion": suggestion,
		"remark":     relation.Remark,
	})
}

// GetMonthlyReport 获取老人月度 AI 报告。
// GET /api/v1/device/monthly-report?elder_id=xxx&month=2026-03
// 安全: 必须校验当前用户与老人存在已确认的亲子绑定关系 (防止 IDOR 越权)。
func GetMonthlyReport(c *gin.Context) {
	userID := c.GetUint64("user_id")
	elderIDStr := c.Query("elder_id")
	month := c.Query("month")

	if elderIDStr == "" || month == "" {
		response.BadRequest(c, "缺少 elder_id 或 month 参数")
		return
	}
	elderID, _ := strconv.ParseUint(elderIDStr, 10, 64)

	// 【IDOR 防护】校验绑定关系
	var relCount int64
	database.DB.Model(&model.UserRelation{}).Where(
		"status = 1 AND elder_id = ? AND youth_id = ?", elderID, userID,
	).Count(&relCount)
	if relCount == 0 {
		response.Fail(c, 403, "无权查看该长辈的报告")
		return
	}

	var report model.MonthlyReport
	result := database.DB.Where("user_id = ? AND report_month = ?", elderID, month).First(&report)

	if result.RowsAffected == 0 {
		response.OK(c, gin.H{
			"status":  "not_ready",
			"message": "本月报告还在生成中，或数据在捉迷藏~",
		})
		return
	}

	response.OK(c, report)
}

// generateInsightSuggestion 调用 LLM 结合当日设备数据生成关切建议，失败回退规则引擎。
func generateInsightSuggestion(ctx context.Context, log model.DeviceStatusLog, remark string) string {
	who := remark
	if who == "" {
		who = "长辈"
	}
	prompt := fmt.Sprintf(
		"你在帮子女解读父母(%s)当天的手机使用与活动数据，用一句话(不超过40字)给出温暖、可行动的关切建议。"+
			"数据：步数%d，屏幕使用%d分钟，解锁%d次，电量%d%%。只输出建议本身。",
		who, log.Steps, log.ScreenUsageMins, log.ScreenUnlocks, log.BatteryLevel,
	)
	out, err := llm.Chat(ctx, []llm.Message{
		{Role: "system", Content: "你是一个关注老人健康的家庭助手。"},
		{Role: "user", Content: prompt},
	})
	if err != nil || out == "" {
		return generateFallbackSuggestion(log)
	}
	return out
}

// generateFallbackSuggestion 当 LLM 不可用时的兜底建议生成 (基于规则)。
func generateFallbackSuggestion(log model.DeviceStatusLog) string {
	if log.Steps < 500 {
		return "今天走动较少，建议提醒长辈出门散散步~"
	}
	if log.ScreenUsageMins > 300 {
		return "今天手机使用时间略长，建议提醒长辈注意休息，保护眼睛~"
	}
	if log.BatteryLevel < 20 {
		return "长辈手机电量偏低，建议提醒及时充电~"
	}
	return "长辈今天状态不错，记得发条消息问候一下哦~"
}
