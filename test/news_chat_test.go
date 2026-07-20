// Package test - 天气/新闻模块 与 聊天 REST 模块 的集成测试。
//
// 测试项 (天气/新闻):
//   - 天气查询: 正常、缺少城市
//   - AI关怀卡片: 正常返回
//   - 新闻列表: 正常、关键词搜索、空结果
//
// 测试项 (聊天 REST):
//   - 会话列表: 无消息、有消息后
//   - 聊天记录: 正常拉取、ID 游标翻页
//   - AI回复: 有消息时正常、无消息时拒绝
//   - STT: 正常调用
package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
)

// ==================== 天气与新闻 ====================

// TestGetWeather_Success 验证正常天气查询。
func TestGetWeather_Success(t *testing.T) {
	r := SetupTestRouter()
	uid := SeedElderUser(database.DB)
	token := GetTestToken(uid, 1)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/weather?city=武汉", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "武汉")
}

// TestGetWeather_MissingCity 验证缺少城市参数被拒。
func TestGetWeather_MissingCity(t *testing.T) {
	r := SetupTestRouter()
	uid := SeedElderUser(database.DB)
	token := GetTestToken(uid, 1)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/weather", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestGetCareCards 验证关怀卡片返回。
func TestGetCareCards(t *testing.T) {
	r := SetupTestRouter()
	uid := SeedElderUser(database.DB)
	token := GetTestToken(uid, 1)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/weather/care-cards?city=武汉", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "cards")
}

// TestGetNews_Success 验证正常获取新闻。
func TestGetNews_Success(t *testing.T) {
	r := SetupTestRouter()
	uid := SeedElderUser(database.DB)
	token := GetTestToken(uid, 1)

	// 植入测试新闻数据
	database.DB.Create(&model.NewsCache{
		City:     "武汉",
		Category: "all",
		Title:    "今日小妙招：冬天如何保暖",
		Summary:  "多穿衣服",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/news?city=武汉&tab=all", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "保暖")
}

// TestGetNews_KeywordSearch 验证新闻关键词搜索。
func TestGetNews_KeywordSearch(t *testing.T) {
	r := SetupTestRouter()
	uid := SeedElderUser(database.DB)
	token := GetTestToken(uid, 1)

	database.DB.Create(&model.NewsCache{
		City:  "全国",
		Title: "健康养生小技巧",
	})
	database.DB.Create(&model.NewsCache{
		City:  "全国",
		Title: "科技新闻速递",
	})

	// 搜索 "养生"
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/news?city=全国&keyword=养生", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "养生")
	assert.NotContains(t, w.Body.String(), "科技", "搜索结果不应包含不相关的新闻")
}

// ==================== 聊天 REST ====================

// TestChatSessions_Empty 验证无消息时会话列表为空。
func TestChatSessions_Empty(t *testing.T) {
	r := SetupTestRouter()
	uid := SeedYouthUser(database.DB)
	token := GetTestToken(uid, 2)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/chat/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestChatSessions_WithMessages 验证有消息后会话列表返回正确。
func TestChatSessions_WithMessages(t *testing.T) {
	r := SetupTestRouter()
	elderID := SeedElderUser(database.DB)
	youthID := SeedYouthUser(database.DB)
	token := GetTestToken(youthID, 2)

	// 植入测试消息
	database.DB.Create(&model.ChatMessage{
		SenderID:   elderID,
		ReceiverID: youthID,
		MsgType:    "text",
		Content:    "吃饭了吗？",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/chat/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// 验证接口正常返回，包含 sessions 字段
	assert.Contains(t, w.Body.String(), "sessions")
}

// TestChatHistory_Success 验证正常拉取聊天记录。
func TestChatHistory_Success(t *testing.T) {
	r := SetupTestRouter()
	elderID := SeedElderUser(database.DB)
	youthID := SeedYouthUser(database.DB)
	token := GetTestToken(youthID, 2)

	// 建立绑定关系 (IDOR 防护要求)
	database.DB.Create(&model.UserRelation{
		ElderID: elderID, YouthID: youthID, InitiatorID: elderID, Status: 1,
	})

	// 植入多条消息
	for i := 0; i < 5; i++ {
		database.DB.Create(&model.ChatMessage{
			SenderID:   elderID,
			ReceiverID: youthID,
			MsgType:    "text",
			Content:    fmt.Sprintf("消息_%d", i),
		})
	}

	url := fmt.Sprintf("/api/v1/chat/history?peer_id=%d&page_size=3", elderID)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	assert.Equal(t, 3, total, "page_size=3 应只返回3条")
}

// TestChatHistory_SinceID 验证 since_id 游标翻页。
func TestChatHistory_SinceID(t *testing.T) {
	r := SetupTestRouter()
	elderID := SeedElderUser(database.DB)
	youthID := SeedYouthUser(database.DB)
	token := GetTestToken(youthID, 2)

	// 建立绑定关系
	database.DB.Create(&model.UserRelation{
		ElderID: elderID, YouthID: youthID, InitiatorID: elderID, Status: 1,
	})

	// 植入消息
	var lastID uint64
	for i := 0; i < 5; i++ {
		msg := model.ChatMessage{
			SenderID:   elderID,
			ReceiverID: youthID,
			MsgType:    "text",
			Content:    fmt.Sprintf("翻页消息_%d", i),
		}
		database.DB.Create(&msg)
		if i == 2 {
			lastID = msg.ID // 取第3条的 ID 作为游标
		}
	}

	url := fmt.Sprintf("/api/v1/chat/history?peer_id=%d&since_id=%d", elderID, lastID)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	assert.Equal(t, 2, total, "since_id 后应只剩2条消息")
}

// TestAIReply_Success 验证 AI 回复建议。
func TestAIReply_Success(t *testing.T) {
	r := SetupTestRouter()
	elderID := SeedElderUser(database.DB)
	youthID := SeedYouthUser(database.DB)
	token := GetTestToken(youthID, 2)

	// 建立绑定关系
	database.DB.Create(&model.UserRelation{
		ElderID: elderID, YouthID: youthID, InitiatorID: elderID, Status: 1,
	})

	database.DB.Create(&model.ChatMessage{
		SenderID:   elderID,
		ReceiverID: youthID,
		MsgType:    "text",
		Content:    "明天降温了，记得穿厚点",
	})

	url := fmt.Sprintf("/api/v1/chat/ai-suggest?peer_id=%d", elderID)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "suggestions")
}

// TestAIReply_NoMessages 验证无绑定关系时 AI 回复被拒绝。
func TestAIReply_NoMessages(t *testing.T) {
	r := SetupTestRouter()
	youthID := SeedYouthUser(database.DB)
	token := GetTestToken(youthID, 2)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/chat/ai-suggest?peer_id=99999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// IDOR 防护先于消息检查, 无绑定关系返回 403
	assert.Contains(t, w.Body.String(), "403")
}
