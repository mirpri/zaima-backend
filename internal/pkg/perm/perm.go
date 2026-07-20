// Package perm 提供跨模块共用的关系与权限判定，避免 handler 与 ws 重复实现。
package perm

import (
	"gorm.io/gorm"

	"zaima-backend/internal/model"
)

// OrderPair 将两个用户 ID 归一化为 (小, 大)，用于好友关系唯一键。
func OrderPair(a, b uint64) (uint64, uint64) {
	if a <= b {
		return a, b
	}
	return b, a
}

// CanChat 判断两个用户之间是否允许聊天。
//
// 允许条件 (满足其一即可):
//   - 存在已确认的亲子绑定关系 (UserRelation.status = 1)
//   - 存在有效的搭子/好友关系 (Friendship.status = 1)
func CanChat(db *gorm.DB, a, b uint64) bool {
	if a == 0 || b == 0 || a == b {
		return false
	}

	var relCount int64
	db.Model(&model.UserRelation{}).Where(
		"status = 1 AND ((elder_id = ? AND youth_id = ?) OR (elder_id = ? AND youth_id = ?))",
		a, b, b, a,
	).Count(&relCount)
	if relCount > 0 {
		return true
	}

	ua, ub := OrderPair(a, b)
	var friendCount int64
	db.Model(&model.Friendship{}).Where(
		"status = 1 AND user_a_id = ? AND user_b_id = ?", ua, ub,
	).Count(&friendCount)
	return friendCount > 0
}

// EnsureFriendship 幂等地在两个用户间建立好友关系 (来源标记 source)。
func EnsureFriendship(db *gorm.DB, a, b uint64, source string) error {
	ua, ub := OrderPair(a, b)
	friend := model.Friendship{
		UserAID: ua,
		UserBID: ub,
		Source:  source,
		Status:  1,
	}
	// 依赖 (user_a_id, user_b_id) 唯一索引保证幂等；已存在则复活为有效。
	var existing model.Friendship
	if err := db.Where("user_a_id = ? AND user_b_id = ?", ua, ub).First(&existing).Error; err == nil {
		if existing.Status != 1 {
			return db.Model(&existing).Update("status", 1).Error
		}
		return nil
	}
	return db.Create(&friend).Error
}
