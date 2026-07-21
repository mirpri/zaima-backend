// Package handler - 聊天相关 API (REST 部分): 会话列表、历史消息、AI回复、STT、创建聊天。
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/llm"
	"zaima-backend/internal/pkg/perm"
	"zaima-backend/internal/pkg/response"
	"zaima-backend/internal/pkg/weather"
)

// ==================== 请求体定义 ====================

// CreateChatReq 创建聊天（发起第一次对话）请求。
type CreateChatReq struct {
	PeerID uint64 `json:"peer_id" binding:"required"` // 对方用户ID
}

// ==================== 响应体定义 ====================

// ChatSession 会话列表项。
type ChatSession struct {
	PeerID      uint64 `json:"peer_id"`       // 对方用户 ID
	PeerName    string `json:"peer_name"`     // 对方昵称
	PeerAvatar  string `json:"peer_avatar"`   // 对方头像
	LastMessage string `json:"last_message"`  // 最后一条消息内容
	LastMsgType string `json:"last_msg_type"` // 最后一条消息类型
	LastTime    string `json:"last_time"`     // 最后消息时间
	UnreadCount int64  `json:"unread_count"`  // 未读消息数
}

// ==================== Handler ====================

// CreateChat 发起第一次对话（创建聊天）。
// POST /api/v1/chat/create
//
// 流程：校验用户关系 -> 检查是否已存在会话 -> 创建初始消息 -> 返回对方信息。
func CreateChat(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req CreateChatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少对方用户ID")
		return
	}

	if req.PeerID == userID {
		response.Fail(c, 4001, "不能与自己聊天")
		return
	}

	// 校验对方用户是否存在
	var peer model.User
	if err := database.DB.First(&peer, req.PeerID).Error; err != nil {
		response.Fail(c, 4002, "用户不存在")
		return
	}

	// 校验是否存在可聊天关系 (亲子绑定 或 广场搭子)
	if !perm.CanChat(database.DB, userID, req.PeerID) {
		response.Fail(c, 4003, "需先建立亲子绑定或搭子关系才能聊天")
		return
	}

	// 检查是否已存在聊天记录 (防止重复创建)
	var msgCount int64
	database.DB.Model(&model.ChatMessage{}).Where(
		"(sender_id = ? AND receiver_id = ?) OR (sender_id = ? AND receiver_id = ?)",
		userID, req.PeerID, req.PeerID, userID,
	).Count(&msgCount)

	// 如果已存在聊天记录，直接返回对方信息 (幂等性)
	if msgCount > 0 {
		response.OK(c, gin.H{
			"peer_id":     peer.ID,
			"peer_name":   peer.Nickname,
			"peer_avatar": peer.AvatarURL,
			"status":      "existing",
			"message":     "聊天已存在",
		})
		return
	}

	// 创建初始化聊天标记消息（可选）
	initMsg := model.ChatMessage{
		SenderID:   userID,
		ReceiverID: req.PeerID,
		MsgType:    "system",
		Content:    "聊天已建立",
		IsRead:     true,
	}
	if err := database.DB.Create(&initMsg).Error; err != nil {
		response.ServerError(c, "创建聊天失败")
		return
	}

	response.OK(c, gin.H{
		"peer_id":     peer.ID,
		"peer_name":   peer.Nickname,
		"peer_avatar": peer.AvatarURL,
		"status":      "created",
		"message":     "聊天已建立",
	})
}

// SendMessageReq 发送消息请求。
type SendMessageReq struct {
	ReceiverID uint64 `json:"receiver_id" binding:"required"`
	Content    string `json:"content" binding:"required"`
	MsgType    string `json:"msg_type"` // text/voice/image, 默认 text
	TempID     string `json:"temp_id"`  // 客户端本地临时ID, 用于回执去重
}

// SendMessage 通过 REST 发送一条消息 (落库 + 在线投递)。
// POST /api/v1/chat/send
//
// 与 WebSocket 发送等价，供不便维持长连接的场景 (如老人端弱网) 使用。
func SendMessage(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req SendMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数不完整")
		return
	}

	// 媒体类型消息校验 URL 合法性 (防 SSRF)
	if (req.MsgType == "voice" || req.MsgType == "image") && !isValidMediaURL(req.Content) {
		response.BadRequest(c, "媒体地址不合法，请使用本平台上传的文件")
		return
	}

	// 【安全】校验可聊天关系
	if !perm.CanChat(database.DB, userID, req.ReceiverID) {
		response.Fail(c, 403, "无权向该用户发送消息")
		return
	}

	if Notifier == nil {
		response.ServerError(c, "消息服务暂不可用")
		return
	}

	msg, err := Notifier.DeliverChat(userID, req.ReceiverID, req.MsgType, req.Content, req.TempID)
	if err != nil {
		response.ServerError(c, "消息发送失败")
		return
	}

	response.OK(c, gin.H{
		"message_id": msg.ID,
		"temp_id":    req.TempID,
		"created_at": msg.CreatedAt,
	})
}

// GetChatSessions 获取聊天会话列表 (首屏)。
// GET /api/v1/chat/sessions?keyword=xxx
//
// 聚合返回：系统消息、关心消息、最近聊天用户列表、最后一条消息及未读数。
func GetChatSessions(c *gin.Context) {
	userID := c.GetUint64("user_id")
	keyword := c.Query("keyword")

	// 1. 查找与当前用户相关的所有最近聊天对象 (去重取最新一条)
	type peerInfo struct {
		PeerID uint64
	}

	// 找到所有聊过天的对方 ID
	var sentPeers, recvPeers []peerInfo
	database.DB.Model(&model.ChatMessage{}).
		Select("DISTINCT receiver_id as peer_id").
		Where("sender_id = ?", userID).
		Scan(&sentPeers)
	database.DB.Model(&model.ChatMessage{}).
		Select("DISTINCT sender_id as peer_id").
		Where("receiver_id = ?", userID).
		Scan(&recvPeers)

	// 合并去重
	peerSet := map[uint64]bool{}
	for _, p := range sentPeers {
		peerSet[p.PeerID] = true
	}
	for _, p := range recvPeers {
		peerSet[p.PeerID] = true
	}

	// 【修复 N+1 查询】批量查询所有 Peer 用户信息，缓存在 map 中
	peerIDs := make([]uint64, 0, len(peerSet))
	for pid := range peerSet {
		peerIDs = append(peerIDs, pid)
	}
	var peerUsers []model.User
	if len(peerIDs) > 0 {
		database.DB.Where("id IN ?", peerIDs).Find(&peerUsers)
	}
	peerMap := make(map[uint64]model.User, len(peerUsers))
	for _, u := range peerUsers {
		peerMap[u.ID] = u
	}

	var sessions []ChatSession
	for peerID := range peerSet {
		peer, exists := peerMap[peerID]
		if !exists {
			continue
		}

		// 关键字过滤 (按昵称搜索)
		if keyword != "" && !containsKeyword(peer.Nickname, keyword) {
			continue
		}

		// 获取最后一条消息
		var lastMsg model.ChatMessage
		database.DB.Where(
			"(sender_id = ? AND receiver_id = ?) OR (sender_id = ? AND receiver_id = ?)",
			userID, peerID, peerID, userID,
		).Order("created_at DESC").First(&lastMsg)

		// 统计未读消息数
		var unreadCount int64
		database.DB.Model(&model.ChatMessage{}).
			Where("sender_id = ? AND receiver_id = ? AND is_read = false", peerID, userID).
			Count(&unreadCount)

		sessions = append(sessions, ChatSession{
			PeerID:      peer.ID,
			PeerName:    peer.Nickname,
			PeerAvatar:  peer.AvatarURL,
			LastMessage: lastMsg.Content,
			LastMsgType: lastMsg.MsgType,
			LastTime:    lastMsg.CreatedAt.Format("2006-01-02 15:04"),
			UnreadCount: unreadCount,
		})
	}

	// 2. 查询系统消息和关心消息的未读数
	var systemUnread, careUnread int64
	database.DB.Model(&model.ChatMessage{}).
		Where("receiver_id = ? AND msg_type = 'system' AND is_read = false", userID).
		Count(&systemUnread)
	database.DB.Model(&model.ChatMessage{}).
		Where("receiver_id = ? AND msg_type = 'care' AND is_read = false", userID).
		Count(&careUnread)

	response.OK(c, gin.H{
		"sessions":      sessions,
		"system_unread": systemUnread,
		"care_unread":   careUnread,
	})
}

// GetChatHistory 获取与指定用户的聊天历史记录。
// GET /api/v1/chat/history?peer_id=xxx&since_id=xxx&page_size=50
func GetChatHistory(c *gin.Context) {
	userID := c.GetUint64("user_id")
	peerIDStr := c.Query("peer_id")
	sinceIDStr := c.Query("since_id") // 消息 ID (用于游标翻页或断线重连拉取)
	pageSizeStr := c.DefaultQuery("page_size", "50")

	if peerIDStr == "" {
		response.BadRequest(c, "缺少 peer_id 参数")
		return
	}

	peerID, err := strconv.ParseUint(peerIDStr, 10, 64)
	if err != nil || peerID == 0 {
		response.BadRequest(c, "peer_id 参数无效")
		return
	}
	pageSize, _ := strconv.Atoi(pageSizeStr)
	if pageSize > 100 {
		pageSize = 100
	}

	// 【安全】校验当前用户与 peer 存在可聊天关系 (防止 IDOR)
	if !perm.CanChat(database.DB, userID, peerID) {
		response.Fail(c, 403, "无权查看与该用户的聊天记录")
		return
	}

	// 按 ID 升序拉取增量消息 (用于断线补发重连)
	query := database.DB.Model(&model.ChatMessage{}).
		Where("(sender_id = ? AND receiver_id = ?) OR (sender_id = ? AND receiver_id = ?)",
			userID, peerID, peerID, userID).
		Order("id ASC")

	if sinceIDStr != "" {
		sinceID, _ := strconv.ParseUint(sinceIDStr, 10, 64)
		if sinceID > 0 {
			query = query.Where("id > ?", sinceID)
		}
	}

	var messages []model.ChatMessage
	query.Limit(pageSize).Find(&messages)

	// 【修复】只标记当前拉取到的消息为已读，而非全部未读
	if len(messages) > 0 {
		readIDs := make([]uint64, 0)
		for _, msg := range messages {
			if msg.SenderID == peerID && !msg.IsRead {
				readIDs = append(readIDs, msg.ID)
			}
		}
		if len(readIDs) > 0 {
			database.DB.Model(&model.ChatMessage{}).
				Where("id IN ?", readIDs).
				Update("is_read", true)
		}
	}

	response.OK(c, gin.H{
		"peer_id":  peerID,
		"total":    len(messages),
		"messages": messages,
	})
}

// AIReply 年轻人端 AI 一键回复建议。
// POST /api/v1/chat/ai-suggest
//
// 根据最近一条父母消息，调用 LLM 生成3-4条15字以内的建议回复。
func AIReply(c *gin.Context) {
	userID := c.GetUint64("user_id")
	peerIDStr := c.Query("peer_id")

	if peerIDStr == "" {
		response.BadRequest(c, "缺少 peer_id 参数")
		return
	}
	peerID, err := strconv.ParseUint(peerIDStr, 10, 64)
	if err != nil || peerID == 0 {
		response.BadRequest(c, "peer_id 参数无效")
		return
	}

	// 【安全】校验当前用户与 peer 存在可聊天关系 (防止 IDOR)
	if !perm.CanChat(database.DB, userID, peerID) {
		response.Fail(c, 403, "无权获取与该用户的 AI 回复建议")
		return
	}

	role := c.GetInt("role")

	// 获取对方最近一条文字消息（可能没有 -> 生成打招呼/话题开场）
	var lastMsg model.ChatMessage
	database.DB.Where("sender_id = ? AND receiver_id = ? AND msg_type = 'text'", peerID, userID).
		Order("created_at DESC").First(&lastMsg)

	suggestions := generateAIReplies(c.Request.Context(), lastMsg.Content, role)

	response.OK(c, gin.H{
		"original_msg": lastMsg.Content,
		"suggestions":  suggestions,
	})
}

// CareSuggest 根据对方所在城市的天气，生成可直接发送的关怀话语。
// GET /api/v1/chat/care-suggest?peer_id=X
func CareSuggest(c *gin.Context) {
	userID := c.GetUint64("user_id")
	role := c.GetInt("role")

	peerID, err := strconv.ParseUint(c.Query("peer_id"), 10, 64)
	if err != nil || peerID == 0 {
		response.BadRequest(c, "peer_id 参数无效")
		return
	}
	if !perm.CanChat(database.DB, userID, peerID) {
		response.Fail(c, 403, "无权限")
		return
	}

	var peer model.User
	if err := database.DB.First(&peer, peerID).Error; err != nil {
		response.Fail(c, 4002, "用户不存在")
		return
	}

	var wd *weather.Data
	if peer.City != "" {
		wd, _ = weather.Fetch(c.Request.Context(), peer.City)
	}

	resp := gin.H{
		"peer_city":   peer.City,
		"suggestions": generateCareSuggestions(c.Request.Context(), role, wd),
	}
	if wd != nil {
		resp["weather"] = wd
	}
	response.OK(c, resp)
}

// generateCareSuggestions 结合对方天气生成关怀话语，失败回退规则。
func generateCareSuggestions(ctx context.Context, role int, wd *weather.Data) []string {
	desc := "天气未知"
	if wd != nil {
		desc = fmt.Sprintf("%s，%s", wd.Text, wd.Temp)
	}
	prompt := fmt.Sprintf("你在帮一位%s给%s发一句天气关怀。对方所在地天气：%s。生成3条温暖、简短(每条≤20字)、可直接发送的关怀话。只输出JSON字符串数组。",
		selfLabel(role), kinLabel(role), desc)

	out, err := llm.Chat(ctx, []llm.Message{
		{Role: "system", Content: "你是贴心的家庭关怀助手，只返回JSON数组。"},
		{Role: "user", Content: prompt},
	})
	if err == nil {
		if s := parseJSONStringArray(out); len(s) > 0 {
			return s
		}
	}
	if wd != nil && wd.Tips != "" {
		return []string{wd.Tips, "记得照顾好自己~", "想你了，有空聊聊"}
	}
	return []string{"最近天气多变，注意身体~", "记得按时吃饭休息", "想你了，有空聊聊"}
}

// selfLabel/kinLabel 根据当前用户角色给出称呼。role: 1=老人, 2=年轻人。
func selfLabel(role int) string {
	if role == 1 {
		return "老人"
	}
	return "在外打拼的年轻人"
}
func kinLabel(role int) string {
	if role == 1 {
		return "孩子"
	}
	return "父母"
}

// generateAIReplies 调用 LLM 生成 3 条建议；无对方消息则生成打招呼开场；不可用时回退规则引擎。
func generateAIReplies(ctx context.Context, peerText string, role int) []string {
	peerText = strings.TrimSpace(peerText)

	var prompt string
	if peerText == "" {
		prompt = fmt.Sprintf("你在帮一位%s主动给%s发消息、开启对话。生成3条温暖、简短(每条不超过18字)、口语化的问候或话题开场。只输出JSON字符串数组。",
			selfLabel(role), kinLabel(role))
	} else {
		prompt = fmt.Sprintf("你在帮一位%s回复%s的消息。针对对方这句话，生成3条温暖、简短(每条不超过18字)、口语化的回复。只输出JSON字符串数组。对方说：%s",
			selfLabel(role), kinLabel(role), peerText)
	}

	out, err := llm.Chat(ctx, []llm.Message{
		{Role: "system", Content: "你是贴心的家庭沟通助手，只返回JSON数组，不要解释。"},
		{Role: "user", Content: prompt},
	})
	if err == nil {
		if replies := parseJSONStringArray(out); len(replies) > 0 {
			return replies
		}
	}
	return generateFallbackReplies(peerText, role)
}

// parseJSONStringArray 从 LLM 输出中提取 JSON 字符串数组 (容忍 ```json 代码块包裹)。
func parseJSONStringArray(s string) []string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(s[start:end+1]), &arr); err != nil {
		return nil
	}
	cleaned := make([]string, 0, len(arr))
	for _, r := range arr {
		if r = strings.TrimSpace(r); r != "" {
			cleaned = append(cleaned, r)
		}
	}
	return cleaned
}

// ==================== 工具函数 ====================

// containsKeyword 判断字符串是否包含关键字 (unicode 安全)。
func containsKeyword(s, keyword string) bool {
	return len(s) > 0 && len(keyword) > 0 &&
		(len(s) >= len(keyword)) &&
		(s == keyword || contains(s, keyword))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// generateFallbackReplies LLM 不可用时的兜底建议（按角色 + 有无对方消息区分）。
func generateFallbackReplies(peerText string, role int) []string {
	if peerText == "" {
		// 打招呼开场
		if role == 1 {
			return []string{"孩子，今天忙不忙？", "记得按时吃饭哦~", "有空回个电话呀"}
		}
		return []string{"爸妈，今天感觉怎么样？", "天气变化记得添衣~", "最近身体还好吗？"}
	}
	// 回复
	if role == 1 {
		return []string{"知道啦，谢谢你~", "你也要照顾好自己", "好的，我记住了"}
	}
	return []string{"收到啦，放心吧~", "好的，我知道了", "谢谢关心，我这边都好"}
}
