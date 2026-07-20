// Package ws 实现基于 WebSocket 的实时聊天连接管理。
//
// 核心组件:
//   - Hub:    维护所有在线连接的注册/注销，并派发消息。
//   - Client: 代表一个 WebSocket 连接，负责读写。
//
// 架构: 每个用户登录后建立一条 WebSocket 连接，服务端通过 Hub.clients
// Map 按 UserID 投递消息，实现点对点聊天。
package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/perm"
)

// ==================== 消息协议 ====================

// WSMessage WebSocket 通信的统一消息格式。
type WSMessage struct {
	Type       string `json:"type"` // 消息类型: chat / ack / system / match_ended
	SenderID   uint64 `json:"sender_id"`
	ReceiverID uint64 `json:"receiver_id"`
	MsgType    string `json:"msg_type"` // text / voice / image / care
	Content    string `json:"content"`
	TempID     string `json:"temp_id"`    // 客户端本地临时消息 ID (用于去重和回调)
	MessageID  uint64 `json:"message_id"` // 服务端生成的全局消息 ID
	Timestamp  int64  `json:"timestamp"`
}

// ==================== Client ====================

// Client 代表一个 WebSocket 客户端连接。
type Client struct {
	UserID uint64
	Conn   *websocket.Conn
	Send   chan []byte // 出站消息缓冲区
	mu     sync.Mutex  // 保护 closed 标志
	closed bool        // 是否已关闭
}

// SafeSend 安全地向客户端发送消息，防止向已关闭的 channel 写入导致 Panic。
func (c *Client) SafeSend(msg []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.Send <- msg:
		return true
	default:
		return false // 缓冲区已满
	}
}

// SafeClose 安全关闭客户端的发送通道，仅关闭一次。
func (c *Client) SafeClose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.Send)
	}
}

// ReadPump 从 WebSocket 连接读取消息并交由 Hub 处理。
// 检测心跳超时：60 秒内未收到任何消息则断开连接。
func (client *Client) ReadPump(hub *Hub) {
	defer func() {
		hub.Unregister <- client
		client.Conn.Close()
	}()

	client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// 【安全】设置消息大小上限 (64KB)，防止恶意超大消息导致 OOM
	client.Conn.SetReadLimit(64 * 1024)

	for {
		_, msgBytes, err := client.Conn.ReadMessage()
		if err != nil {
			break
		}

		// 刷新读超时
		client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			log.Printf("[ws] 消息解析失败: %v", err)
			continue
		}

		msg.SenderID = client.UserID
		msg.Timestamp = time.Now().UnixMilli()

		hub.Broadcast <- msg
	}
}

// WritePump 将 Hub 推送的消息写入 WebSocket 连接。
// 每30秒发送一次 Ping 帧保持连接活跃。
func (client *Client) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			if !ok {
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ==================== Hub ====================

// Hub 管理所有在线 WebSocket 连接，负责消息路由。
type Hub struct {
	// clients 在线用户连接表: UserID -> Client
	clients map[uint64]*Client
	mu      sync.RWMutex

	// Register 新连接注册通道
	Register chan *Client
	// Unregister 连接注销通道
	Unregister chan *Client
	// Broadcast 消息广播通道
	Broadcast chan WSMessage
}

// NewHub 创建一个新的 Hub 实例。
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[uint64]*Client),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Broadcast:  make(chan WSMessage, 256),
	}
}

// Run 启动 Hub 的事件循环 (应在独立 goroutine 中运行)。
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			// 如果该用户已有旧连接，安全关闭旧连接
			if old, exists := h.clients[client.UserID]; exists {
				old.SafeClose()
			}
			h.clients[client.UserID] = client
			h.mu.Unlock()
			log.Printf("[ws] 用户 %d 已上线, 当前在线: %d", client.UserID, len(h.clients))

		case client := <-h.Unregister:
			h.mu.Lock()
			if _, exists := h.clients[client.UserID]; exists {
				delete(h.clients, client.UserID)
				client.SafeClose()
			}
			h.mu.Unlock()
			log.Printf("[ws] 用户 %d 已离线, 当前在线: %d", client.UserID, len(h.clients))

		case msg := <-h.Broadcast:
			h.handleMessage(msg)
		}
	}
}

// handleMessage 处理收到的消息：持久化 -> 投递给在线用户或走离线推送。
func (h *Hub) handleMessage(msg WSMessage) {
	switch msg.Type {
	case "chat":
		h.handleChatMessage(msg)
	case "ack":
		h.handleACK(msg)
	default:
		log.Printf("[ws] 未知消息类型: %s", msg.Type)
	}
}

// handleChatMessage 处理聊天消息：鉴权 + 入库 + 投递。
func (h *Hub) handleChatMessage(msg WSMessage) {
	// 【安全】校验发送者与接收者之间是否存在合法关系 (亲子绑定或搭子好友)
	if msg.ReceiverID == 0 {
		log.Printf("[ws] 消息被拒: receiver_id 为空")
		return
	}
	if !perm.CanChat(database.DB, msg.SenderID, msg.ReceiverID) {
		log.Printf("[ws] 消息被拒: 用户 %d 与 %d 无聊天关系", msg.SenderID, msg.ReceiverID)
		return
	}

	// 1. 持久化 + 在线投递 (与 REST /chat/send 共用同一逻辑)
	chatMsg, err := h.persistAndDeliver(msg.SenderID, msg.ReceiverID, msg.MsgType, msg.Content, msg.TempID)
	if err != nil {
		return
	}

	// 2. 回传 ACK 给发送方 (确认服务端已收到并持久化)
	h.mu.RLock()
	sender, senderOnline := h.clients[msg.SenderID]
	h.mu.RUnlock()

	if senderOnline {
		ack := WSMessage{
			Type:      "ack",
			MessageID: chatMsg.ID,
			TempID:    msg.TempID,
			Timestamp: time.Now().UnixMilli(),
		}
		ackBytes, _ := json.Marshal(ack)
		sender.SafeSend(ackBytes)
	}
}

// persistAndDeliver 持久化一条聊天消息并尝试在线投递给接收方。
// 供 WebSocket 与 REST /chat/send 共用；不做关系鉴权 (调用方负责)。
func (h *Hub) persistAndDeliver(senderID, receiverID uint64, msgType, content, tempID string) (*model.ChatMessage, error) {
	if msgType == "" {
		msgType = "text"
	}
	chatMsg := model.ChatMessage{
		SenderID:   senderID,
		ReceiverID: receiverID,
		MsgType:    msgType,
		Content:    content,
	}
	if err := database.DB.Create(&chatMsg).Error; err != nil {
		log.Printf("[ws] 消息持久化失败: %v", err)
		return nil, err
	}

	deliver := WSMessage{
		Type:       "chat",
		SenderID:   senderID,
		ReceiverID: receiverID,
		MsgType:    msgType,
		Content:    content,
		TempID:     tempID,
		MessageID:  chatMsg.ID,
		Timestamp:  chatMsg.CreatedAt.UnixMilli(),
	}
	msgBytes, _ := json.Marshal(deliver)

	h.mu.RLock()
	receiver, online := h.clients[receiverID]
	h.mu.RUnlock()

	if online {
		receiver.SafeSend(msgBytes)
	} else {
		// 对方不在线：当前仅做在线推送，离线消息由对方上线后 /chat/history 增量拉取补齐。
		log.Printf("[ws] 用户 %d 不在线，消息已入库待其上线拉取", receiverID)
	}
	return &chatMsg, nil
}

// DeliverChat 是 persistAndDeliver 的导出封装，供 handler 层 (REST /chat/send) 调用。
func (h *Hub) DeliverChat(senderID, receiverID uint64, msgType, content, tempID string) (*model.ChatMessage, error) {
	return h.persistAndDeliver(senderID, receiverID, msgType, content, tempID)
}

// PushEvent 向指定用户推送一个通用事件 (绑定通知、匹配结束、陌生设备告警等)。
// 仅在用户在线时投递 (在线推送模式)。
func (h *Hub) PushEvent(userID uint64, eventType string, data interface{}) {
	payload := map[string]interface{}{
		"type":      eventType,
		"data":      data,
		"timestamp": time.Now().UnixMilli(),
	}
	msgBytes, _ := json.Marshal(payload)

	h.mu.RLock()
	client, online := h.clients[userID]
	h.mu.RUnlock()

	if online {
		client.SafeSend(msgBytes)
	}
}

// handleACK 处理客户端的已读确认。
// 【安全】校验消息接收者必须是当前用户，防止伪造 ACK 标记他人消息已读。
func (h *Hub) handleACK(msg WSMessage) {
	if msg.MessageID > 0 {
		database.DB.Model(&model.ChatMessage{}).
			Where("id = ? AND receiver_id = ?", msg.MessageID, msg.SenderID).
			Update("is_read", true)
	}
}

// SendToUser 主动向指定用户推送消息 (供其他模块调用)。
func (h *Hub) SendToUser(userID uint64, msg WSMessage) {
	msgBytes, _ := json.Marshal(msg)

	h.mu.RLock()
	client, online := h.clients[userID]
	h.mu.RUnlock()

	if online {
		client.SafeSend(msgBytes)
	}
}

// IsOnline 检查用户是否在线。
func (h *Hub) IsOnline(userID uint64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[userID]
	return ok
}
