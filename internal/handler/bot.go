// Package handler - AI 陪伴助手"小暖"：注入人设的情感陪伴对话。
package handler

import (
	"github.com/gin-gonic/gin"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/llm"
	"zaima-backend/internal/pkg/response"
)

// botPersona 陪伴助手的人设(系统提示词)。
const botPersona = "你是'在吗'App里的AI陪伴助手，名叫'小暖'。你温柔、耐心、有共情力，" +
	"像家人一样陪用户聊天、倾听心事、宽慰情绪。多关心用户的心情和生活起居，适时给点温暖的鼓励。" +
	"回复要简短、口语化、亲切(每次不超过60字)，不要长篇大论，也不要像客服。用中文回复。"

// BotSendReq 给机器人发消息请求。
type BotSendReq struct {
	Content string `json:"content" binding:"required"`
}

// GetBotHistory 获取当前用户与陪伴助手的对话历史。
// GET /api/v1/chat/bot/history
func GetBotHistory(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var msgs []model.BotMessage
	database.DB.Where("user_id = ?", userID).Order("id ASC").Limit(100).Find(&msgs)
	response.OK(c, gin.H{"messages": msgs})
}

// BotSend 向陪伴助手发消息并获得回复。
// POST /api/v1/chat/bot/send
func BotSend(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req BotSendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "说点什么吧")
		return
	}

	// 1. 存用户消息
	database.DB.Create(&model.BotMessage{UserID: userID, Role: "user", Content: req.Content})

	// 2. 取最近对话作为上下文 (最多最近 20 条)
	var recent []model.BotMessage
	database.DB.Where("user_id = ?", userID).Order("id DESC").Limit(20).Find(&recent)
	// 反转为时间正序
	msgs := []llm.Message{{Role: "system", Content: botPersona}}
	for i := len(recent) - 1; i >= 0; i-- {
		msgs = append(msgs, llm.Message{Role: recent[i].Role, Content: recent[i].Content})
	}

	// 3. 调 LLM，失败给温暖兜底
	reply, err := llm.Chat(c.Request.Context(), msgs)
	if err != nil || reply == "" {
		reply = botFallback(req.Content)
	}

	// 4. 存助手回复
	database.DB.Create(&model.BotMessage{UserID: userID, Role: "assistant", Content: reply})

	response.OK(c, gin.H{"reply": reply})
}

// botFallback LLM 不可用时的温暖兜底回复。
func botFallback(_ string) string {
	return "我在呢，一直都在。今天过得怎么样，愿意和我聊聊吗？"
}
