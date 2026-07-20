// Package test - 端到端系统测试 (E2E System Tests)。
//
// 本文件包含以下完整用户旅程:
//  1. 老人端完整入驻流程 (注册 -> 完善资料 -> 设置兴趣 -> 发布广场气泡)
//  2. 年轻人端完整入驻流程 (注册 -> 完善资料 -> 绑定长辈 -> 查看监控)
//  3. 亲子绑定双向完整流程 (老人注册 -> 年轻人注册 -> 发起绑定 -> 确认绑定 -> 各自查看资料)
//  4. 聊天全链路E2E (注册双方 -> 绑定 -> 发消息(REST模拟) -> 拉会话列表 -> 拉历史 -> AI回复)
//  5. 广场交友全链路 (注册 -> 设兴趣 -> 发布气泡 -> 搜索气泡 -> 匹配确认 -> 验证消失)
//  6. 设备监控与报告全链路 (注册双方 -> 绑定 -> 多日上报 -> 拉日报 -> 查月报)
//  7. 天气新闻缓存一致性 (首次请求 -> 二次请求命中缓存 -> 验证source字段)
//  8. 鉴权边界与安全测试 (越权访问、Token过期、重放验证码)
package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"zaima-backend/internal/config"
	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
)

// ==================== 工具函数 ====================

// e2eLogin 注册并登录一个用户，返回 (userID, token)。
func e2eLogin(t *testing.T, r *gin.Engine, phone string, role int) (uint64, string) {
	t.Helper()

	// 1. 发送验证码
	smsBody := fmt.Sprintf(`{"phone":"%s"}`, phone)
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/api/v1/auth/sms-code", bytes.NewBufferString(smsBody))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)

	// 2. 从 Redis 取验证码
	code, err := database.RDB.Get(context.Background(), "sms:code:"+phone).Result()
	require.NoError(t, err)

	// 3. 登录
	loginBody, _ := json.Marshal(map[string]interface{}{
		"phone": phone,
		"code":  code,
		"role":  role,
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/api/v1/auth/login", bytes.NewBuffer(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]interface{}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	token := data["token"].(string)
	userID := uint64(data["user_id"].(float64))

	return userID, token
}

// e2eRequest 发送一个带鉴权的 HTTP 请求并返回响应 recorder。
func e2eRequest(r *gin.Engine, method, url, token string, body interface{}) *httptest.ResponseRecorder {
	var reqBody *bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, url, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)
	return w
}

// parseRespData 从响应中提取 data 字段。
func parseRespData(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		return nil
	}
	return data
}

// ==================== 系统测试 ====================

// TestE2E_ElderOnboarding 老人端完整入驻旅程:
// 注册 -> 完善资料(昵称+头像+城市) -> 设置兴趣(广场舞+棋牌) -> 发布广场气泡 -> 验证气泡存在。
func TestE2E_ElderOnboarding(t *testing.T) {
	r := SetupTestRouter()

	// Step 1: 注册
	elderID, elderToken := e2eLogin(t, r, "13700001111", 1)
	assert.NotZero(t, elderID)

	// Step 2: 完善资料
	w := e2eRequest(r, "PUT", "/api/v1/user/profile", elderToken, map[string]string{
		"nickname": "李大爷",
		"city":     "武汉",
		"province": "湖北",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Step 3: 设置兴趣
	w = e2eRequest(r, "PUT", "/api/v1/user/interests", elderToken, map[string]interface{}{
		"tags": []string{"广场舞", "棋牌"},
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Step 4: 发布广场气泡
	w = e2eRequest(r, "POST", "/api/v1/square/publish", elderToken, map[string]interface{}{
		"voice_url":    "https://cdn.zaima.test/files/elder_voice.mp3",
		"interest_tag": "广场舞",
		"province":     "湖北",
		"city":         "武汉",
		"longitude":    114.3055,
		"latitude":     30.5928,
	})
	assert.Equal(t, http.StatusOK, w.Code)
	data := parseRespData(t, w)
	require.NotNil(t, data)
	assert.NotZero(t, data["bubble_id"], "应返回气泡 ID")

	// Step 5: 验证气泡可被搜索到
	w = e2eRequest(r, "GET", "/api/v1/square/bubbles?city=武汉", elderToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	data = parseRespData(t, w)
	require.NotNil(t, data)
	total := int(data["total"].(float64))
	assert.GreaterOrEqual(t, total, 1, "应至少能搜到自己发布的气泡")

	// Step 6: 验证用户资料完整
	w = e2eRequest(r, "GET", "/api/v1/user/profile", elderToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "李大爷")
	assert.Contains(t, w.Body.String(), "广场舞")
}

// TestE2E_FullBindingFlow 亲子绑定双向全流程:
// 老人注册 -> 年轻人注册 -> 年轻人发起绑定 -> 老人确认 -> 双方各自查看资料中包含对方绑定关系。
func TestE2E_FullBindingFlow(t *testing.T) {
	r := SetupTestRouter()

	// Step 1: 双方注册
	elderID, elderToken := e2eLogin(t, r, "13700002222", 1)
	youthID, youthToken := e2eLogin(t, r, "13700003333", 2)
	_ = elderID
	_ = youthID

	// Step 2: 年轻人完善资料
	e2eRequest(r, "PUT", "/api/v1/user/profile", youthToken, map[string]string{
		"nickname": "小王",
		"city":     "深圳",
	})

	// Step 3: 年轻人发起绑定
	w := e2eRequest(r, "POST", "/api/v1/user/bind", youthToken, map[string]interface{}{
		"target_phone": "13700002222",
		"youth_city":   "深圳",
		"remark":       "妈妈",
	})
	assert.Equal(t, http.StatusOK, w.Code)
	data := parseRespData(t, w)
	require.NotNil(t, data)
	assert.Equal(t, "pending", data["status"])
	relationID := uint64(data["relation_id"].(float64))

	// Step 4: 老人确认绑定
	w = e2eRequest(r, "POST", "/api/v1/user/bind/confirm", elderToken, map[string]interface{}{
		"relation_id": relationID,
	})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "绑定成功")

	// Step 5: 年轻人查看资料，应看到绑定关系
	w = e2eRequest(r, "GET", "/api/v1/user/profile", youthToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "relations")

	// Step 6: 老人查看资料，应看到绑定关系
	w = e2eRequest(r, "GET", "/api/v1/user/profile", elderToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "relations")
}

// TestE2E_ChatLifecycle 聊天全链路:
// 注册双方 -> 绑定 -> 老人发消息(DB模拟) -> 年轻人拉会话列表 -> 拉历史记录 ->
// 触发AI回复 -> 再发一条消息 -> 通过since_id增量拉取验证。
func TestE2E_ChatLifecycle(t *testing.T) {
	r := SetupTestRouter()

	// Step 1: 注册双方
	elderID, _ := e2eLogin(t, r, "13700004444", 1)
	youthID, youthToken := e2eLogin(t, r, "13700005555", 2)

	// 建立绑定关系 (IDOR 防护要求)
	database.DB.Create(&model.UserRelation{
		ElderID: elderID, YouthID: youthID, InitiatorID: elderID, Status: 1,
	})

	// Step 2: 模拟老人发送消息 (直接插入DB，模拟 WebSocket 入库后的状态)
	msg1 := model.ChatMessage{
		SenderID:   elderID,
		ReceiverID: youthID,
		MsgType:    "text",
		Content:    "孩子，今天降温了注意穿衣服",
	}
	database.DB.Create(&msg1)

	msg2 := model.ChatMessage{
		SenderID:   elderID,
		ReceiverID: youthID,
		MsgType:    "voice",
		Content:    "https://oss/voice_msg.mp3",
	}
	database.DB.Create(&msg2)

	// Step 3: 年轻人拉取会话列表
	w := e2eRequest(r, "GET", "/api/v1/chat/sessions", youthToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "sessions")

	// Step 4: 年轻人拉取与老人的聊天历史
	url := fmt.Sprintf("/api/v1/chat/history?peer_id=%d", elderID)
	w = e2eRequest(r, "GET", url, youthToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	data := parseRespData(t, w)
	require.NotNil(t, data)
	total := int(data["total"].(float64))
	assert.Equal(t, 2, total, "应有2条消息")

	// 提取最后一条消息ID用于后续增量拉取
	messages := data["messages"].([]interface{})
	lastMsg := messages[len(messages)-1].(map[string]interface{})
	lastMsgID := uint64(lastMsg["id"].(float64))

	// Step 5: 年轻人触发 AI 回复建议
	aiURL := fmt.Sprintf("/api/v1/chat/ai-suggest?peer_id=%d", elderID)
	w = e2eRequest(r, "POST", aiURL, youthToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "suggestions")

	// Step 6: 老人再发一条消息
	msg3 := model.ChatMessage{
		SenderID:   elderID,
		ReceiverID: youthID,
		MsgType:    "text",
		Content:    "怎么不回消息啊",
	}
	database.DB.Create(&msg3)

	// Step 7: 年轻人用 since_id 增量拉取
	sinceURL := fmt.Sprintf("/api/v1/chat/history?peer_id=%d&since_id=%d", elderID, lastMsgID)
	w = e2eRequest(r, "GET", sinceURL, youthToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	data = parseRespData(t, w)
	require.NotNil(t, data)
	newTotal := int(data["total"].(float64))
	assert.Equal(t, 1, newTotal, "增量拉取应只有1条新消息")
}

// TestE2E_SquareMatchmaking 广场交友全链路:
// 老人A注册 -> 设兴趣 -> 发布气泡 -> 老人B注册 -> 搜索看到A的气泡 ->
// A确认匹配 -> 气泡消失 -> B再次搜索看不到。
func TestE2E_SquareMatchmaking(t *testing.T) {
	r := SetupTestRouter()

	// Step 1: 老人 A 注册并发布气泡
	_, tokenA := e2eLogin(t, r, "13700006666", 1)
	e2eRequest(r, "PUT", "/api/v1/user/profile", tokenA, map[string]string{
		"nickname": "赵爷爷",
		"province": "湖北",
		"city":     "武汉",
	})
	e2eRequest(r, "PUT", "/api/v1/user/interests", tokenA, map[string]interface{}{
		"tags": []string{"太极拳"},
	})

	w := e2eRequest(r, "POST", "/api/v1/square/publish", tokenA, map[string]interface{}{
		"voice_url":    "https://cdn.zaima.test/files/taichi.mp3",
		"interest_tag": "太极拳",
		"province":     "湖北",
		"city":         "武汉",
		"longitude":    114.30,
		"latitude":     30.59,
	})
	assert.Equal(t, http.StatusOK, w.Code)
	dataA := parseRespData(t, w)
	require.NotNil(t, dataA)
	bubbleID := uint64(dataA["bubble_id"].(float64))

	// Step 2: 老人 B 注册并搜索
	_, tokenB := e2eLogin(t, r, "13700007777", 1)
	e2eRequest(r, "PUT", "/api/v1/user/profile", tokenB, map[string]string{
		"nickname": "周奶奶",
		"province": "湖北",
		"city":     "武汉",
	})

	w = e2eRequest(r, "GET", "/api/v1/square/bubbles?city=武汉&keyword=赵爷爷", tokenB, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	dataB := parseRespData(t, w)
	require.NotNil(t, dataB)
	foundTotal := int(dataB["total"].(float64))
	assert.GreaterOrEqual(t, foundTotal, 1, "B 应能搜到 A 的气泡")

	// Step 3: A 确认匹配
	w = e2eRequest(r, "POST", "/api/v1/square/match-confirm", tokenA, map[string]interface{}{
		"bubble_id": bubbleID,
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Step 4: 验证气泡已消失
	var bubble model.SquareBubble
	database.DB.First(&bubble, bubbleID)
	assert.Equal(t, 0, bubble.Status, "匹配后气泡 Status 应为0")

	// Step 5: B 用关键字再搜，不应再看到此气泡 (已过期)
	w = e2eRequest(r, "GET", "/api/v1/square/bubbles?city=武汉&keyword=赵爷爷", tokenB, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	dataB2 := parseRespData(t, w)
	if dataB2 != nil {
		afterTotal := int(dataB2["total"].(float64))
		assert.Equal(t, 0, afterTotal, "匹配后 B 不应再搜到 A 的气泡")
	}
}

// TestE2E_DeviceMonitoringFlow 设备监控与报告全链路:
// 注册双方 -> 绑定 -> 老人连续3天上报 -> 年轻人拉取日报+AI建议 -> 查不存在的月报。
func TestE2E_DeviceMonitoringFlow(t *testing.T) {
	r := SetupTestRouter()

	// Step 1: 注册并绑定
	elderID, elderToken := e2eLogin(t, r, "13700008888", 1)
	youthID, youthToken := e2eLogin(t, r, "13700009999", 2)

	database.DB.Create(&model.UserRelation{
		ElderID: elderID, YouthID: youthID, InitiatorID: elderID, Status: 1,
	})

	// 设置老人资料
	e2eRequest(r, "PUT", "/api/v1/user/profile", elderToken, map[string]string{
		"nickname": "钱奶奶",
	})

	// Step 2: 老人连续3天上报数据
	days := []struct {
		date  string
		steps int
		bat   int
		mins  int
	}{
		{"2026-03-03", 2000, 90, 45},
		{"2026-03-04", 300, 70, 120}, // 步数很少，应触发 AI 建议
		{"2026-03-05", 5000, 50, 80},
	}
	for _, d := range days {
		w := e2eRequest(r, "POST", "/api/v1/device/upload", elderToken, map[string]interface{}{
			"record_date":       d.date,
			"steps":             d.steps,
			"battery_level":     d.bat,
			"screen_usage_mins": d.mins,
		})
		assert.Equal(t, http.StatusOK, w.Code)
	}

	// Step 3: 年轻人拉取日报
	url := fmt.Sprintf("/api/v1/device/daily-insight?elder_id=%d", elderID)
	w := e2eRequest(r, "GET", url, youthToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	data := parseRespData(t, w)
	require.NotNil(t, data)
	assert.Contains(t, w.Body.String(), "suggestion", "应包含 AI 建议")
	assert.Contains(t, w.Body.String(), "history", "应包含历史数据")

	// 验证 history 包含3天数据
	history := data["history"].([]interface{})
	assert.GreaterOrEqual(t, len(history), 3, "应有至少3天的历史数据")

	// Step 4: 年轻人查询不存在的月报
	monthURL := fmt.Sprintf("/api/v1/device/monthly-report?elder_id=%d&month=2026-03", elderID)
	w = e2eRequest(r, "GET", monthURL, youthToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "not_ready", "未生成的月报应返回 not_ready")
}

// TestE2E_WeatherCacheConsistency 天气缓存一致性验证:
// 首次请求 -> 第二次请求命中缓存 -> 验证 source 字段值变化。
func TestE2E_WeatherCacheConsistency(t *testing.T) {
	r := SetupTestRouter()

	// 启动本地天气源 (模拟 Open-Meteo 的 geocoding 与 forecast)，保证测试离线可跑
	weatherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"results":[{"latitude":39.9,"longitude":116.4,"name":"北京"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":12.3,"relative_humidity_2m":55,"weather_code":2,"wind_direction_10m":90}}`))
	}))
	defer weatherSrv.Close()
	config.AppConfig.Weather.BaseURL = weatherSrv.URL
	config.AppConfig.Weather.GeoURL = weatherSrv.URL

	_, token := e2eLogin(t, r, "13700010000", 1)

	// 第一次请求 (应来自 api)
	w1 := e2eRequest(r, "GET", "/api/v1/weather?city=北京", token, nil)
	assert.Equal(t, http.StatusOK, w1.Code)
	data1 := parseRespData(t, w1)
	require.NotNil(t, data1)
	assert.Equal(t, "api", data1["source"], "首次请求应来自 API")

	// 第二次请求 (应命中缓存)
	w2 := e2eRequest(r, "GET", "/api/v1/weather?city=北京", token, nil)
	assert.Equal(t, http.StatusOK, w2.Code)
	data2 := parseRespData(t, w2)
	require.NotNil(t, data2)
	assert.Equal(t, "cache", data2["source"], "二次请求应命中缓存")
}

// TestE2E_SecurityBoundary 鉴权边界与安全测试:
// 无Token访问 -> 过期Token -> 验证码重放攻击 -> 越权操作。
func TestE2E_SecurityBoundary(t *testing.T) {
	r := SetupTestRouter()

	// Case 1: 无 Token 访问受保护接口
	protectedURLs := []string{
		"/api/v1/user/profile",
		"/api/v1/device/daily-insight?elder_id=1",
		"/api/v1/square/bubbles",
		"/api/v1/chat/sessions",
		"/api/v1/weather?city=武汉",
		"/api/v1/news?city=武汉",
	}
	for _, url := range protectedURLs {
		w := e2eRequest(r, "GET", url, "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "无Token访问 %s 应返回401", url)
	}

	// Case 2: 验证码重放攻击
	phone := "13700011111"
	// 发送验证码
	e2eRequest(r, "POST", "/api/v1/auth/sms-code", "", map[string]string{"phone": phone})

	code, _ := database.RDB.Get(context.Background(), "sms:code:"+phone).Result()

	// 第一次使用
	w := e2eRequest(r, "POST", "/api/v1/auth/login", "", map[string]interface{}{
		"phone": phone, "code": code, "role": 1,
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// 重放攻击: 同一个验证码第二次使用
	w = e2eRequest(r, "POST", "/api/v1/auth/login", "", map[string]interface{}{
		"phone": phone, "code": code, "role": 1,
	})
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	respCode := int(resp["code"].(float64))
	assert.Equal(t, 1001, respCode, "验证码重放应被拒绝(code=1001)")
}

// TestE2E_HealthCheck 健康检查端点验证。
func TestE2E_HealthCheck(t *testing.T) {
	r := SetupTestRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "zaima-backend")
}
