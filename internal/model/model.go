// Package model 定义了所有数据库实体模型。
// 每个结构体对应 PostgreSQL 中的一张表，使用 GORM 注解标记字段映射。
package model

import (
	"time"
)

// ==================== 用户与关系 ====================

// User 用户总表，存储老人和年轻人的基本身份信息。
type User struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Phone     string    `gorm:"type:varchar(20);uniqueIndex;not null" json:"phone"`
	Role      int       `gorm:"type:smallint;not null;comment:1=老人,2=年轻人" json:"role"`
	Nickname  string    `gorm:"type:varchar(32);default:''" json:"nickname"`
	AvatarURL string    `gorm:"type:varchar(512);default:''" json:"avatar_url"`
	City      string    `gorm:"type:varchar(64);default:''" json:"city"`
	Province  string    `gorm:"type:varchar(64);default:''" json:"province"`
	DeviceID  string    `gorm:"type:varchar(128);default:''" json:"device_id"` // 最近登录的设备标识
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName 指定用户表名。
func (User) TableName() string { return "users" }

// UserRelation 亲子关系绑定表。
type UserRelation struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ElderID     uint64    `gorm:"uniqueIndex:idx_elder_youth;not null;comment:老人用户ID" json:"elder_id"`
	YouthID     uint64    `gorm:"uniqueIndex:idx_elder_youth;not null;comment:年轻人用户ID" json:"youth_id"`
	InitiatorID uint64    `gorm:"not null;comment:发起绑定的用户ID,用于防止自我确认" json:"initiator_id"`
	Status      int       `gorm:"type:smallint;default:0;comment:0=等待确认,1=已绑定" json:"status"`
	YouthCity   string    `gorm:"type:varchar(64);default:'';comment:年轻人所在城市" json:"youth_city"`
	ElderAddr   string    `gorm:"type:varchar(256);default:'';comment:老人详细地址" json:"elder_addr"`
	Remark      string    `gorm:"type:varchar(32);default:'';comment:子女对长辈的备注" json:"remark"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName 指定亲子关系表名。
func (UserRelation) TableName() string { return "user_relations" }

// UserInterest 用户兴趣标签 (老人端选择，最多3个)。
type UserInterest struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint64    `gorm:"index;not null" json:"user_id"`
	InterestTag string    `gorm:"type:varchar(32);not null;comment:兴趣标签如棋牌、广场舞" json:"interest_tag"`
	Status      int       `gorm:"type:smallint;default:1;index;comment:1=有效,0=历史" json:"status"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TableName 指定兴趣标签表名。
func (UserInterest) TableName() string { return "user_interests" }

// Friendship 搭子/好友关系表 (广场陌生人社交建立的关系)。
//
// 与 UserRelation (亲子绑定) 并列: 二者任一存在即可聊天。
// 为保证唯一性, UserAID 恒为较小的用户 ID, UserBID 恒为较大的用户 ID。
type Friendship struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserAID   uint64    `gorm:"uniqueIndex:idx_friend_pair;not null;comment:较小的用户ID" json:"user_a_id"`
	UserBID   uint64    `gorm:"uniqueIndex:idx_friend_pair;not null;comment:较大的用户ID" json:"user_b_id"`
	Source    string    `gorm:"type:varchar(16);default:'square';comment:来源:square/other" json:"source"`
	Status    int       `gorm:"type:smallint;default:1;comment:1=有效,0=已解除" json:"status"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName 指定好友关系表名。
func (Friendship) TableName() string { return "friendships" }

// ==================== 设备状态监控 ====================

// DeviceStatusLog 老人端每日上报的设备状态数据 (步数、电量、屏幕使用)。
type DeviceStatusLog struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID          uint64    `gorm:"uniqueIndex:idx_user_date;not null" json:"user_id"`
	RecordDate      string    `gorm:"type:date;not null;uniqueIndex:idx_user_date" json:"record_date"` // 格式: 2026-03-05
	Steps           int       `gorm:"default:0" json:"steps"`
	BatteryLevel    int       `gorm:"default:0;comment:电量百分比" json:"battery_level"`
	ScreenUnlocks   int       `gorm:"default:0;comment:屏幕解锁次数" json:"screen_unlocks"`
	ScreenUsageMins int       `gorm:"default:0;comment:屏幕使用时长(分钟)" json:"screen_usage_mins"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TableName 指定设备状态日志表名。
func (DeviceStatusLog) TableName() string { return "device_status_logs" }

// MonthlyReport AI 月度报告存储表 (每月1号生成)。
type MonthlyReport struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint64    `gorm:"index;not null" json:"user_id"`
	ReportMonth string    `gorm:"type:varchar(7);not null;index;comment:格式2026-03" json:"report_month"`
	InsightData string    `gorm:"type:jsonb;comment:AI分析JSON" json:"insight_data"` // JSONB
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TableName 指定月度报告表名。
func (MonthlyReport) TableName() string { return "monthly_reports" }

// ==================== 聊天消息 ====================

// ChatMessage 聊天消息持久化表。
type ChatMessage struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	SenderID   uint64    `gorm:"index;index:idx_chat_members;not null" json:"sender_id"`
	ReceiverID uint64    `gorm:"index;index:idx_chat_members;not null" json:"receiver_id"`
	MsgType    string    `gorm:"type:varchar(16);not null;comment:text/voice/image/system/care" json:"msg_type"`
	Content    string    `gorm:"type:text;not null;comment:文字内容或OSS文件URL" json:"content"`
	IsRead     bool      `gorm:"default:false" json:"is_read"`
	CreatedAt  time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

// TableName 指定聊天消息表名。
func (ChatMessage) TableName() string { return "chat_messages" }

// ==================== 广场气泡 ====================

// SquareBubble 广场 "一起玩" 气泡表。
type SquareBubble struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      uint64    `gorm:"index;not null" json:"user_id"`
	Nickname    string    `gorm:"type:varchar(32);not null" json:"nickname"`
	AvatarURL   string    `gorm:"type:varchar(512);default:''" json:"avatar_url"`
	VoiceURL    string    `gorm:"type:varchar(512);not null;comment:OSS录音URL" json:"voice_url"`
	InterestTag string    `gorm:"type:varchar(32);not null;comment:活动兴趣标签" json:"interest_tag"`
	Province    string    `gorm:"type:varchar(64);default:''" json:"province"`
	City        string    `gorm:"type:varchar(64);default:''" json:"city"`
	Longitude   float64   `gorm:"type:double precision;default:0" json:"longitude"`
	Latitude    float64   `gorm:"type:double precision;default:0" json:"latitude"`
	Status      int       `gorm:"type:smallint;default:1;index:idx_status_expire;comment:1=活跃,0=已消失" json:"status"`
	ExpireAt    time.Time `gorm:"not null;index:idx_status_expire;comment:自动过期时间(4h后)" json:"expire_at"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TableName 指定广场气泡表名。
func (SquareBubble) TableName() string { return "square_bubbles" }

// ==================== 新闻缓存 ====================

// NewsCache 新闻缓存表，用于避免频繁请求第三方 API。
type NewsCache struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	City      string    `gorm:"type:varchar(64);index;not null" json:"city"`
	Category  string    `gorm:"type:varchar(32);default:'all';comment:全部/最新/热点" json:"category"`
	Title     string    `gorm:"type:varchar(256);not null" json:"title"`
	Summary   string    `gorm:"type:text;default:''" json:"summary"`
	ImageURL  string    `gorm:"type:varchar(512);default:''" json:"image_url"`
	SourceURL string    `gorm:"type:varchar(512);default:''" json:"source_url"`
	PubDate   string    `gorm:"type:date" json:"pub_date"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// TableName 指定新闻缓存表名。
func (NewsCache) TableName() string { return "news_cache" }
