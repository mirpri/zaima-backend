// Package handler - "一起玩"广场气泡发布、发现、搜索与确认匹配。
package handler

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/perm"
	"zaima-backend/internal/pkg/response"
)

// ==================== 请求体定义 ====================

// PublishBubbleReq 发布气泡请求。
type PublishBubbleReq struct {
	VoiceURL    string  `json:"voice_url" binding:"required"` // OSS 录音文件 URL
	InterestTag string  `json:"interest_tag" binding:"required"`
	Province    string  `json:"province"`
	City        string  `json:"city"`
	Longitude   float64 `json:"longitude" binding:"required"`
	Latitude    float64 `json:"latitude" binding:"required"`
}

// MatchConfirmReq 确认匹配请求。
type MatchConfirmReq struct {
	BubbleID uint64 `json:"bubble_id" binding:"required"`
}

// StartChatReq 发起搭子聊天请求。
type StartChatReq struct {
	PeerID uint64 `json:"peer_id" binding:"required"` // 想结识的广场用户ID
}

// ==================== 响应体定义 ====================

// SquareUserInfo 广场用户信息（带兴趣标签）。
type SquareUserInfo struct {
	UserID    uint64   `json:"user_id"`
	Nickname  string   `json:"nickname"`
	AvatarURL string   `json:"avatar_url"`
	City      string   `json:"city"`
	Province  string   `json:"province"`
	Interests []string `json:"interests"` // 兴趣标签列表
}

// ==================== Handler ====================

// PublishBubble 发布广场气泡。
// POST /api/v1/square/publish
//
// 流程：保存气泡到 DB -> 将经纬度写入 Redis GEO -> 设置4小时 TTL。
func PublishBubble(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req PublishBubbleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数不完整")
		return
	}

	// 查询用户信息 (昵称、头像)
	var user model.User
	database.DB.First(&user, userID)

	// 【安全】URL 校验，防止 SSRF / 任意外链
	if !isValidMediaURL(req.VoiceURL) {
		response.BadRequest(c, "语音 URL 不合法，请使用本平台上传的文件地址")
		return
	}

	bubble := model.SquareBubble{
		UserID:      userID,
		Nickname:    user.Nickname,
		AvatarURL:   user.AvatarURL,
		VoiceURL:    req.VoiceURL,
		InterestTag: req.InterestTag,
		Province:    req.Province,
		City:        req.City,
		Longitude:   req.Longitude,
		Latitude:    req.Latitude,
		Status:      1, // 活跃
		ExpireAt:    time.Now().Add(4 * time.Hour),
	}

	if err := database.DB.Create(&bubble).Error; err != nil {
		response.ServerError(c, "发布失败，请稍后重试")
		return
	}

	// 写入 Redis GEO 用于地理位置范围查询
	ctx := context.Background()
	geoKey := "square:geo"
	memberKey := fmt.Sprintf("bubble:%d", bubble.ID)

	database.RDB.GeoAdd(ctx, geoKey, &redis.GeoLocation{
		Name:      memberKey,
		Longitude: req.Longitude,
		Latitude:  req.Latitude,
	})
	// 设置4小时后自动过期 (使用单独的 key 标记 TTL)
	database.RDB.Set(ctx, fmt.Sprintf("square:ttl:%d", bubble.ID), "1", 4*time.Hour)

	response.OKWithMsg(c, "发布成功", gin.H{"bubble_id": bubble.ID})
}

// GetBubbles 获取广场气泡列表。
// GET /api/v1/square/bubbles?lng=xxx&lat=xxx&radius=5000&province=xxx&city=xxx&keyword=xxx
//
// 支持：GEO 半径查询 + 省份/城市筛选 + 举办人昵称模糊搜索。
func GetBubbles(c *gin.Context) {
	lngStr := c.DefaultQuery("lng", "0")
	latStr := c.DefaultQuery("lat", "0")
	radiusStr := c.DefaultQuery("radius", "10000") // 默认10km
	province := c.Query("province")
	city := c.Query("city")
	keyword := c.Query("keyword")

	lng, _ := strconv.ParseFloat(lngStr, 64)
	lat, _ := strconv.ParseFloat(latStr, 64)
	radius, _ := strconv.ParseFloat(radiusStr, 64)

	ctx := context.Background()
	var bubbleIDs []uint64

	// 1. 优先使用 GEO 查询附近气泡
	if lng != 0 && lat != 0 {
		results, err := database.RDB.GeoRadius(ctx, "square:geo", lng, lat, &redis.GeoRadiusQuery{
			Radius: radius,
			Unit:   "m",
			Sort:   "ASC",
			Count:  50,
		}).Result()
		if err == nil {
			for _, loc := range results {
				var id uint64
				_, _ = fmt.Sscanf(loc.Name, "bubble:%d", &id)
				if id > 0 {
					// 检查是否已过期
					exists, _ := database.RDB.Exists(ctx, fmt.Sprintf("square:ttl:%d", id)).Result()
					if exists > 0 {
						bubbleIDs = append(bubbleIDs, id)
					}
				}
			}
		}
	}

	// 2. 构建数据库查询
	query := database.DB.Model(&model.SquareBubble{}).Where("status = 1 AND expire_at > ?", time.Now())

	// 【修复 GEO 击穿】如果传入了 lng/lat 但附近没有气泡，应直接返回空列表
	geoSearched := lng != 0 && lat != 0
	if geoSearched && len(bubbleIDs) == 0 {
		// 附近无人，直接返回空
		response.OK(c, gin.H{"total": 0, "bubbles": []interface{}{}})
		return
	}
	if len(bubbleIDs) > 0 {
		query = query.Where("id IN ?", bubbleIDs)
	}

	// 省份/城市筛选
	if province != "" {
		query = query.Where("province = ?", province)
	}
	if city != "" {
		query = query.Where("city = ?", city)
	}

	// 举办人昵称模糊搜索
	if keyword != "" {
		query = query.Where("nickname LIKE ?", "%"+keyword+"%")
	}

	var bubbles []model.SquareBubble
	query.Order("created_at DESC").Limit(20).Find(&bubbles)

	response.OK(c, gin.H{
		"total":   len(bubbles),
		"bubbles": bubbles,
	})
}

// MatchConfirm 确认匹配 (发布者找到玩伴)。
// POST /api/v1/square/match-confirm
//
// 流程：标记气泡为已消失 -> 删除 Redis GEO 数据 -> 通知其他等待者聊天结束。
func MatchConfirm(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req MatchConfirmReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少 bubble_id")
		return
	}

	var bubble model.SquareBubble
	if err := database.DB.First(&bubble, req.BubbleID).Error; err != nil {
		response.Fail(c, 2001, "气泡不存在")
		return
	}

	// 只有发布者能确认
	if bubble.UserID != userID {
		response.Fail(c, 2002, "无权操作此气泡")
		return
	}

	// 标记为已消失
	database.DB.Model(&bubble).Update("status", 0)

	// 清除 Redis GEO 和 TTL 数据
	ctx := context.Background()
	memberKey := fmt.Sprintf("bubble:%d", bubble.ID)
	database.RDB.ZRem(ctx, "square:geo", memberKey)
	database.RDB.Del(ctx, fmt.Sprintf("square:ttl:%d", bubble.ID))

	// 在线通知发布者本人：气泡已结束 (前端可据此更新UI)
	notify(userID, "match_ended", gin.H{"bubble_id": bubble.ID})

	response.OKWithMsg(c, "匹配成功，气泡已消失", nil)
}

// SquareStartChat 在广场发起搭子聊天：建立好友关系，允许双方开始对话。
// POST /api/v1/square/start-chat
//
// 这是陌生人社交的入口——广场上没有亲子绑定，通过此接口建立 Friendship 后才可聊天。
func SquareStartChat(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req StartChatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少对方用户ID")
		return
	}
	if req.PeerID == userID {
		response.Fail(c, 2003, "不能和自己交朋友")
		return
	}

	var peer model.User
	if err := database.DB.First(&peer, req.PeerID).Error; err != nil {
		response.Fail(c, 2004, "用户不存在")
		return
	}

	// 建立/激活好友关系 (幂等)
	if err := perm.EnsureFriendship(database.DB, userID, req.PeerID, "square"); err != nil {
		response.ServerError(c, "建立搭子关系失败")
		return
	}

	// 首次结识时插入一条系统消息，作为会话起点 (避免重复插入)
	var msgCount int64
	database.DB.Model(&model.ChatMessage{}).Where(
		"(sender_id = ? AND receiver_id = ?) OR (sender_id = ? AND receiver_id = ?)",
		userID, req.PeerID, req.PeerID, userID,
	).Count(&msgCount)
	if msgCount == 0 {
		database.DB.Create(&model.ChatMessage{
			SenderID:   userID,
			ReceiverID: req.PeerID,
			MsgType:    "system",
			Content:    "你们成为搭子啦，打个招呼吧~",
			IsRead:     true,
		})
	}

	// 在线通知对方：多了一个新搭子
	notify(req.PeerID, "new_friend", gin.H{"peer_id": userID})

	response.OK(c, gin.H{
		"peer_id":     peer.ID,
		"peer_name":   peer.Nickname,
		"peer_avatar": peer.AvatarURL,
	})
}

// GetSquareUsers 获取广场用户列表（带兴趣标签）。
// GET /api/v1/square/users?keyword=xxx&interest=xxx&page=1&page_size=20
//
// 返回所有有活跃气泡或满足过滤条件的用户，包含头像和兴趣标签。
func GetSquareUsers(c *gin.Context) {
	keyword := c.Query("keyword")      // 用户昵称搜索
	interestTag := c.Query("interest") // 兴趣标签过滤
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("page_size", "20")

	page, _ := strconv.Atoi(pageStr)
	pageSize, _ := strconv.Atoi(pageSizeStr)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize
	now := time.Now()

	// 1. 先圈定所有有活跃气泡的用户，兴趣可匹配气泡标签或用户资料标签。
	activeUserSubQuery := database.DB.Model(&model.SquareBubble{}).
		Select("DISTINCT square_bubbles.user_id").
		Where("square_bubbles.status = 1 AND square_bubbles.expire_at > ?", now)
	if interestTag != "" {
		activeUserSubQuery = activeUserSubQuery.
			Joins("LEFT JOIN user_interests ON user_interests.user_id = square_bubbles.user_id AND user_interests.status = 1").
			Where("(square_bubbles.interest_tag = ? OR user_interests.interest_tag = ?)", interestTag, interestTag)
	}

	// 2. 查询用户信息并统计过滤后的总数
	var total int64
	countQuery := database.DB.Model(&model.User{}).Where("id IN (?)", activeUserSubQuery)
	if keyword != "" {
		countQuery = countQuery.Where("nickname LIKE ?", "%"+keyword+"%")
	}
	countQuery.Count(&total)

	var users []model.User
	userQuery := database.DB.Where("id IN (?)", activeUserSubQuery)
	if keyword != "" {
		userQuery = userQuery.Where("nickname LIKE ?", "%"+keyword+"%")
	}
	userQuery.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&users)

	// 3. 为每个用户查询兴趣标签
	result := make([]SquareUserInfo, 0, len(users))
	for _, user := range users {
		var interests []model.UserInterest
		database.DB.Where("user_id = ? AND status = 1", user.ID).Order("id ASC").Find(&interests)
		tags := make([]string, len(interests))
		for i, item := range interests {
			tags[i] = item.InterestTag
		}

		result = append(result, SquareUserInfo{
			UserID:    user.ID,
			Nickname:  user.Nickname,
			AvatarURL: user.AvatarURL,
			City:      user.City,
			Province:  user.Province,
			Interests: tags,
		})
	}

	response.OK(c, gin.H{
		"total": total,
		"page":  page,
		"users": result,
	})
}
