// Package handler - 用户信息完善与亲子绑定。
package handler

import (
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/response"
)

// ==================== 请求体定义 ====================

// UpdateProfileReq 更新用户资料请求。
type UpdateProfileReq struct {
	Nickname  string    `json:"nickname"`   // 昵称 (≤8字)
	AvatarURL string    `json:"avatar_url"` // 头像 HTTPS URL
	City      string    `json:"city"`       // 所在城市
	Province  string    `json:"province"`   // 所在省份
	Interests *[]string `json:"interests"`  // 兴趣标签 (最多3个，可选)
}

// BindReq 亲子绑定请求。
type BindReq struct {
	TargetPhone string `json:"target_phone" binding:"required"` // 对方手机号
	YouthCity   string `json:"youth_city"`                      // 年轻人所在城市
	ElderAddr   string `json:"elder_addr"`                      // 老人所在详细地址
	Remark      string `json:"remark"`                          // 备注 (如 "妈妈")
}

// BindConfirmReq 确认绑定请求。
type BindConfirmReq struct {
	RelationID uint64 `json:"relation_id" binding:"required"`
}

// UpdateInterestsReq 更新兴趣标签请求。
type UpdateInterestsReq struct {
	Tags []string `json:"tags" binding:"required"` // 最多3个标签
}

// ==================== Handler ====================

// GetProfile 获取当前用户资料。
// GET /api/v1/user/profile
func GetProfile(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		response.Fail(c, 1002, "用户不存在")
		return
	}

	// 查询绑定关系
	var relations []model.UserRelation
	if user.Role == 1 {
		database.DB.Where("elder_id = ?", userID).Find(&relations)
	} else {
		database.DB.Where("youth_id = ?", userID).Find(&relations)
	}

	// 查询兴趣标签
	var interests []model.UserInterest
	database.DB.Where("user_id = ? AND status = 1", userID).Order("id ASC").Find(&interests)
	tags := make([]string, len(interests))
	for i, item := range interests {
		tags[i] = item.InterestTag
	}

	response.OK(c, gin.H{
		"user":      user,
		"relations": relations,
		"interests": tags,
	})
}

// UpdateProfile 更新用户资料 (昵称、头像、城市、兴趣等)。
// PUT /api/v1/user/profile
func UpdateProfile(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误")
		return
	}

	// 昵称长度校验 (≤8个 UTF-8 字符)
	if req.Nickname != "" && len([]rune(req.Nickname)) > 8 {
		response.Fail(c, 1003, "昵称不可多于8字")
		return
	}

	// 兴趣标签校验 (最多3个)
	if req.Interests != nil && len(*req.Interests) > 3 {
		response.Fail(c, 1006, "最多选择3个兴趣标签")
		return
	}

	updates := map[string]interface{}{}
	if req.Nickname != "" {
		updates["nickname"] = req.Nickname
	}
	if req.AvatarURL != "" {
		// 头像允许: 本平台上传的地址, 或第三方 HTTPS 头像地址
		if !isValidMediaURL(req.AvatarURL) && !isValidAvatarURL(req.AvatarURL) {
			response.BadRequest(c, "头像 URL 不合法")
			return
		}
		updates["avatar_url"] = req.AvatarURL
	}
	if req.City != "" {
		updates["city"] = req.City
	}
	if req.Province != "" {
		updates["province"] = req.Province
	}

	// 使用事务包裹更新，确保用户资料和兴趣标签一致性
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		// 更新用户基本信息
		if len(updates) > 0 {
			if err := tx.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
				return err
			}
		}

		// 如果提供了兴趣标签，则更新
		if req.Interests != nil {
			if err := replaceActiveInterests(tx, userID, *req.Interests); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		response.ServerError(c, "资料更新失败")
		return
	}

	response.OKWithMsg(c, "资料已更新", nil)
}

// BindRequest 发起亲子绑定请求。
// POST /api/v1/user/bind
//
// 流程：通过手机号查找对方 -> 对方已注册则发推送请求确认 -> 未注册则返回提示发短信邀请。
func BindRequest(c *gin.Context) {
	userID := c.GetUint64("user_id")
	role := c.GetInt("role")

	var req BindReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入对方手机号")
		return
	}

	// 查询对方是否已注册
	var target model.User
	result := database.DB.Where("phone = ?", req.TargetPhone).First(&target)

	if result.RowsAffected == 0 {
		// 对方未注册
		response.OK(c, gin.H{
			"status":  "not_registered",
			"message": "对方还没安装'在吗'，请发短信邀请",
		})
		return
	}

	// 【安全】检查是否已存在同一对用户的 pending/已绑定记录
	var existCount int64
	relation := model.UserRelation{
		Status:      0, // 等待确认
		InitiatorID: userID,
		YouthCity:   req.YouthCity,
		ElderAddr:   req.ElderAddr,
		Remark:      req.Remark,
	}
	if role == 1 {
		relation.ElderID = userID
		relation.YouthID = target.ID
		database.DB.Model(&model.UserRelation{}).Where(
			"elder_id = ? AND youth_id = ? AND status IN (0,1)", userID, target.ID,
		).Count(&existCount)
	} else {
		relation.YouthID = userID
		relation.ElderID = target.ID
		database.DB.Model(&model.UserRelation{}).Where(
			"elder_id = ? AND youth_id = ? AND status IN (0,1)", target.ID, userID,
		).Count(&existCount)
	}
	if existCount > 0 {
		response.Fail(c, 1010, "已存在绑定请求，请勿重复发起")
		return
	}

	if err := database.DB.Create(&relation).Error; err != nil {
		response.ServerError(c, "绑定请求创建失败")
		return
	}

	// 在线通知对方有新的绑定请求待确认
	notify(target.ID, "bind_request", gin.H{
		"relation_id":  relation.ID,
		"initiator_id": userID,
		"remark":       req.Remark,
		"message":      "有一个新的亲情绑定请求，请确认",
	})

	response.OK(c, gin.H{
		"status":      "pending",
		"relation_id": relation.ID,
		"message":     "绑定请求已发送，请等待对方确认",
	})
}

// BindConfirm 确认亲子绑定。
// POST /api/v1/user/bind/confirm
func BindConfirm(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req BindConfirmReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少绑定ID")
		return
	}

	var relation model.UserRelation
	if err := database.DB.First(&relation, req.RelationID).Error; err != nil {
		response.Fail(c, 1004, "绑定请求不存在")
		return
	}

	// 【幂等性】已绑定的绑定不能再次确认
	if relation.Status == 1 {
		response.Fail(c, 1009, "已经绑定，请勿重复操作")
		return
	}

	// 确认只有被邀请方能确认 (不能是发起方自己确认自己)
	if relation.ElderID != userID && relation.YouthID != userID {
		response.Fail(c, 1005, "无权操作此绑定请求")
		return
	}
	if relation.InitiatorID == userID {
		response.Fail(c, 1007, "发起方不能自己确认绑定请求")
		return
	}

	// 更新状态为已绑定
	database.DB.Model(&relation).Update("status", 1)

	// 在线通知发起方绑定成功 (前端可展示庆祝动效)
	notify(relation.InitiatorID, "bind_confirmed", gin.H{
		"relation_id": relation.ID,
		"message":     "对方已确认绑定，亲情连接成功！",
	})

	response.OKWithMsg(c, "绑定成功！", gin.H{"relation_id": relation.ID})
}

// UpdateInterests 更新用户兴趣标签 (老人端，最多3个)。
// PUT /api/v1/user/interests
func UpdateInterests(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var req UpdateInterestsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请选择兴趣标签")
		return
	}

	if len(req.Tags) > 3 {
		response.Fail(c, 1006, "最多选择3个兴趣标签")
		return
	}

	// 使用事务包裹失效旧标签+批量插入，确保数据一致性
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		return replaceActiveInterests(tx, userID, req.Tags)
	})
	if err != nil {
		response.ServerError(c, "兴趣标签更新失败")
		return
	}

	response.OKWithMsg(c, "兴趣标签已更新", nil)
}

// FamilyMember 已绑定家人信息。
type FamilyMember struct {
	UserID    uint64     `json:"user_id"`
	Nickname  string     `json:"nickname"`
	AvatarURL string     `json:"avatar_url"`
	Phone     string     `json:"phone"`
	Role      int        `json:"role"`
	Remark    string     `json:"remark"`
	City      string     `json:"city"`
	LastLogin *time.Time `json:"last_login"` // 可能为空(从未登录)
}

// GetFamily 获取当前用户已绑定的家人列表(含昵称/头像/电话/上次登录)。
// GET /api/v1/user/family
func GetFamily(c *gin.Context) {
	userID := c.GetUint64("user_id")

	var relations []model.UserRelation
	database.DB.Where("status = 1 AND (elder_id = ? OR youth_id = ?)", userID, userID).Find(&relations)

	members := make([]FamilyMember, 0, len(relations))
	for _, r := range relations {
		peerID := r.ElderID
		if peerID == userID {
			peerID = r.YouthID
		}
		var u model.User
		if err := database.DB.First(&u, peerID).Error; err != nil {
			continue
		}
		var last *time.Time
		if !u.LastLoginAt.IsZero() {
			t := u.LastLoginAt
			last = &t
		}
		members = append(members, FamilyMember{
			UserID:    u.ID,
			Nickname:  u.Nickname,
			AvatarURL: u.AvatarURL,
			Phone:     u.Phone,
			Role:      u.Role,
			Remark:    r.Remark,
			City:      u.City,
			LastLogin: last,
		})
	}

	response.OK(c, gin.H{"family": members})
}

// ==================== 工具函数 ====================

func replaceActiveInterests(tx *gorm.DB, userID uint64, tags []string) error {
	if err := tx.Model(&model.UserInterest{}).
		Where("user_id = ? AND status = 1", userID).
		Update("status", 0).Error; err != nil {
		return err
	}

	interests := make([]model.UserInterest, len(tags))
	for i, tag := range tags {
		interests[i] = model.UserInterest{
			UserID:      userID,
			InterestTag: tag,
			Status:      1,
		}
	}
	if len(interests) == 0 {
		return nil
	}
	return tx.Create(&interests).Error
}

func isValidAvatarURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if parsed.Scheme != "https" || host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil {
		return false
	}
	return strings.Contains(host, ".")
}
