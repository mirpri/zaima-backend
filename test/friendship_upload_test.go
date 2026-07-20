// Package test - 搭子关系、广场发起聊天、REST 发消息、自建文件上传的集成测试。
package test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"zaima-backend/internal/config"
	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	"zaima-backend/internal/pkg/perm"
	"zaima-backend/internal/scheduler"
)

// TestCanChat_RelationAndFriendship 验证亲子绑定与搭子关系均可聊天。
func TestCanChat_RelationAndFriendship(t *testing.T) {
	SetupTestRouter()
	a := SeedElderUser(database.DB)
	b := SeedYouthUser(database.DB)

	// 初始无任何关系
	assert.False(t, perm.CanChat(database.DB, a, b), "无关系不应可聊天")

	// 建立搭子关系后可聊天
	require.NoError(t, perm.EnsureFriendship(database.DB, a, b, "square"))
	assert.True(t, perm.CanChat(database.DB, a, b), "搭子关系应可聊天")
	assert.True(t, perm.CanChat(database.DB, b, a), "关系应双向")

	// 幂等: 重复建立不报错、不重复插入
	require.NoError(t, perm.EnsureFriendship(database.DB, b, a, "square"))
	var cnt int64
	database.DB.Model(&model.Friendship{}).Count(&cnt)
	assert.Equal(t, int64(1), cnt, "好友关系应唯一")
}

// TestSquareStartChat_CreatesFriendshipAndAllowsSend 验证广场发起聊天全流程。
func TestSquareStartChat_CreatesFriendshipAndAllowsSend(t *testing.T) {
	r := SetupTestRouter()
	me := SeedElderUser(database.DB)
	peer := SeedYouthUser(database.DB)
	token := GetTestToken(me, 1)

	// 发起前：不能直接发消息 (无关系)
	w := e2eRequest(r, "POST", "/api/v1/chat/send", token, map[string]interface{}{
		"receiver_id": peer, "content": "在吗", "msg_type": "text",
	})
	assert.Contains(t, w.Body.String(), "无权", "无关系不应能发消息")

	// 广场发起搭子聊天
	w = e2eRequest(r, "POST", "/api/v1/square/start-chat", token, map[string]interface{}{
		"peer_id": peer,
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// 关系建立后可发消息
	w = e2eRequest(r, "POST", "/api/v1/chat/send", token, map[string]interface{}{
		"receiver_id": peer, "content": "你好呀", "msg_type": "text",
	})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "message_id")

	// 消息应已落库
	var msgCount int64
	database.DB.Model(&model.ChatMessage{}).
		Where("sender_id = ? AND receiver_id = ? AND content = ?", me, peer, "你好呀").
		Count(&msgCount)
	assert.Equal(t, int64(1), msgCount)
}

// TestSendMessage_RejectInvalidMediaURL 验证媒体消息的 URL 校验。
func TestSendMessage_RejectInvalidMediaURL(t *testing.T) {
	r := SetupTestRouter()
	me := SeedElderUser(database.DB)
	peer := SeedYouthUser(database.DB)
	require.NoError(t, perm.EnsureFriendship(database.DB, me, peer, "square"))
	token := GetTestToken(me, 1)

	// 非法媒体地址 (内网) 应被拒
	w := e2eRequest(r, "POST", "/api/v1/chat/send", token, map[string]interface{}{
		"receiver_id": peer, "content": "http://169.254.169.254/x.mp3", "msg_type": "voice",
	})
	assert.Contains(t, w.Body.String(), "不合法")

	// 合法自建存储地址应通过
	w = e2eRequest(r, "POST", "/api/v1/chat/send", token, map[string]interface{}{
		"receiver_id": peer, "content": "https://cdn.zaima.test/files/v.mp3", "msg_type": "voice",
	})
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestUploadFile_Success 验证自建文件上传成功并返回可访问 URL。
func TestUploadFile_Success(t *testing.T) {
	r := SetupTestRouter()
	config.AppConfig.Storage.Dir = t.TempDir()
	uid := SeedElderUser(database.DB)
	token := GetTestToken(uid, 1)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("file", "avatar.png")
	// 最小 PNG 头 + 少量数据
	part.Write([]byte("\x89PNG\r\n\x1a\n-fake-image-bytes"))
	_ = mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	url := data["url"].(string)
	assert.Contains(t, url, "/files/uploads/users/")
	assert.Contains(t, url, ".png")
}

// TestUploadFile_RejectUnsupportedType 验证不支持的文件类型被拒。
func TestUploadFile_RejectUnsupportedType(t *testing.T) {
	r := SetupTestRouter()
	config.AppConfig.Storage.Dir = t.TempDir()
	uid := SeedElderUser(database.DB)
	token := GetTestToken(uid, 1)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("file", "evil.exe")
	part.Write([]byte("MZ-executable"))
	_ = mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Contains(t, w.Body.String(), "不支持的文件类型")
}

// TestScheduler_CleanExpiredBubbles 验证过期气泡清理逻辑 (直接调度函数)。
func TestScheduler_CleanExpiredBubbles(t *testing.T) {
	SetupTestRouter()
	uid := SeedElderUser(database.DB)

	// 造一个已过期气泡
	database.DB.Create(&model.SquareBubble{
		UserID: uid, Nickname: "张大爷", VoiceURL: "x", InterestTag: "测试",
		Status: 1, ExpireAt: time.Now().Add(-time.Hour),
	})

	scheduler.CleanExpiredBubbles(context.Background())

	var active int64
	database.DB.Model(&model.SquareBubble{}).Where("status = 1").Count(&active)
	assert.Equal(t, int64(0), active, "过期气泡应被标记为消失")
}
