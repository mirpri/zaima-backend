// Package handler - 实时通知能力的注入点。
package handler

import "zaima-backend/internal/model"

// RealtimeNotifier 抽象 WebSocket Hub 的在线推送能力，供 handler 解耦调用。
// 由 main/router 在启动时注入具体实现 (*ws.Hub)。
type RealtimeNotifier interface {
	// PushEvent 向用户推送通用事件 (绑定/匹配/告警等)。
	PushEvent(userID uint64, eventType string, data interface{})
	// DeliverChat 持久化并在线投递一条聊天消息。
	DeliverChat(senderID, receiverID uint64, msgType, content, tempID string) (*model.ChatMessage, error)
	// IsOnline 查询用户是否在线。
	IsOnline(userID uint64) bool
}

// Notifier 全局实时通知器，可能为 nil (如未启用 WS 的测试场景)，调用前需判空。
var Notifier RealtimeNotifier

// notify 安全地推送事件，Notifier 未注入时静默跳过。
func notify(userID uint64, eventType string, data interface{}) {
	if Notifier != nil {
		Notifier.PushEvent(userID, eventType, data)
	}
}
