// Package handler 实现所有 HTTP API 的请求处理。
// 本文件负责：短信验证码发送、登录/注册、设备校验。
package handler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/config"
	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/response"
	"zaima-backend/internal/pkg/utils"
)

// ==================== 请求体定义 ====================

// SendCodeReq 发送验证码请求体。
type SendCodeReq struct {
	Phone string `json:"phone" binding:"required"` // 手机号 (11位)
}

// LoginReq 登录/注册请求体。
type LoginReq struct {
	Phone    string `json:"phone" binding:"required,len=11"`
	Code     string `json:"code" binding:"required"`           // 短信验证码
	Role     int    `json:"role" binding:"required,oneof=1 2"` // 1=老人, 2=年轻人
	DeviceID string `json:"device_id"`                         // 设备标识 (可选)
}

// ==================== Handler ====================

// SendSMSCode 发送短信验证码。
// POST /api/v1/auth/sms-code
//
// 流程：生成6位验证码 -> 存入 Redis (5分钟有效) -> 调用短信 API 发送。
// 开发环境下验证码直接返回，方便联调。
func SendSMSCode(c *gin.Context) {
	var req SendCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入正确的手机号")
		return
	}

	// 校验手机号格式 (简单校验11位数字)
	if len(req.Phone) != 11 {
		response.BadRequest(c, "手机号必须为11位")
		return
	}

	// 【短信频控】每个手机号 60 秒内只能发送一次 (防短信轰炸)
	lockKey := fmt.Sprintf("sms:lock:%s", req.Phone)
	locked, _ := database.RDB.SetNX(context.Background(), lockKey, "1", 60*time.Second).Result()
	if !locked {
		response.Fail(c, 1008, "发送太频繁，请 60 秒后重试")
		return
	}

	// 生成验证码并存入 Redis，有效期5分钟
	code := utils.GenerateSMSCode()
	redisKey := fmt.Sprintf("sms:code:%s", req.Phone)
	database.RDB.Set(context.Background(), redisKey, code, 5*time.Minute)

	// TODO: 接入阿里云短信 SDK 实际发送验证码
	// 开发环境下直接返回验证码方便调试
	log.Printf("[auth] 验证码 -> %s: %s", req.Phone, code)

	if config.AppConfig.Server.Mode == "debug" {
		response.OK(c, gin.H{"code": code, "msg": "开发模式，验证码直接返回"})
	} else {
		response.OKWithMsg(c, "验证码已发送", nil)
	}
}

// Login 登录/注册 (手机号+验证码)。
// POST /api/v1/auth/login
//
// 流程：校验验证码 -> 查询/创建用户 -> 检测陌生设备 -> 生成 JWT。
func Login(c *gin.Context) {
	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数不完整")
		return
	}

	// 1. 校验验证码
	redisKey := fmt.Sprintf("sms:code:%s", req.Phone)
	cachedCode, err := database.RDB.Get(context.Background(), redisKey).Result()
	if err != nil || cachedCode != req.Code {
		response.Fail(c, 1001, "验证码错误或已过期")
		return
	}
	// 验证通过后立即删除验证码，防止重放
	database.RDB.Del(context.Background(), redisKey)

	// 2. 查询用户是否已存在，不存在则自动注册
	var user model.User
	result := database.DB.Where("phone = ?", req.Phone).First(&user)
	isNewUser := result.RowsAffected == 0

	if isNewUser {
		user = model.User{
			Phone: req.Phone,
			Role:  req.Role,
		}
		if err := database.DB.Create(&user).Error; err != nil {
			response.ServerError(c, "注册失败，请稍后重试")
			return
		}
	}

	// 3. 陌生设备检测 (老人端)
	if user.Role == 1 && req.DeviceID != "" && user.DeviceID != "" && user.DeviceID != req.DeviceID {
		// 老人从新设备登录 -> 通知绑定的年轻人
		go notifyYouthStrangeDevice(user.ID, req.DeviceID)
	}

	// 更新设备 ID 与最近登录时间
	updates := map[string]interface{}{"last_login_at": time.Now()}
	if req.DeviceID != "" {
		updates["device_id"] = req.DeviceID
	}
	database.DB.Model(&user).Updates(updates)

	// 4. 生成 JWT Token
	token, err := utils.GenerateToken(
		user.ID, user.Phone, user.Role,
		config.AppConfig.JWT.Secret,
		config.AppConfig.JWT.ExpireHours,
	)
	if err != nil {
		response.ServerError(c, "Token 生成失败")
		return
	}

	response.OK(c, gin.H{
		"token":    token,
		"user_id":  user.ID,
		"role":     user.Role,
		"nickname": user.Nickname,
		"is_new":   isNewUser,
	})
}

// notifyYouthStrangeDevice 异步通知年轻人端：老人从陌生设备登录。
func notifyYouthStrangeDevice(elderID uint64, newDeviceID string) {
	var relations []model.UserRelation
	database.DB.Where("elder_id = ? AND status = 1", elderID).Find(&relations)
	for _, rel := range relations {
		log.Printf("[auth] 陌生设备告警: 老人 %d 从设备 %s 登录, 通知年轻人 %d",
			elderID, newDeviceID, rel.YouthID)
		notify(rel.YouthID, "stranger_device", gin.H{
			"elder_id":  elderID,
			"device_id": newDeviceID,
			"message":   "检测到长辈从新设备登录，请留意安全",
		})
	}
}
