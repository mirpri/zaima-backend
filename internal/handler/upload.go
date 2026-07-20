// Package handler - 自建文件存储：上传与静态访问 (替代第三方 OSS)。
package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/config"
	"zaima-backend/internal/pkg/response"
)

// allowedUploadTypes 允许上传的 MIME 类型 (图片与音频)。
var allowedUploadTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"audio/mpeg": ".mp3",
	"audio/wav":  ".wav",
	"audio/wave": ".wav",
	"audio/ogg":  ".ogg",
	"audio/mp4":  ".m4a",
	"audio/aac":  ".aac",
}

// storageDir 返回配置的本地存储根目录 (默认 ./data/uploads)。
func storageDir() string {
	if config.AppConfig != nil && config.AppConfig.Storage.Dir != "" {
		return config.AppConfig.Storage.Dir
	}
	return "./data/uploads"
}

// storagePublicBaseURL 返回对外访问前缀 (去除尾部斜杠)。
func storagePublicBaseURL() string {
	if config.AppConfig != nil {
		return strings.TrimRight(config.AppConfig.Storage.PublicBaseURL, "/")
	}
	return ""
}

// maxUploadBytes 返回单文件大小上限 (默认 10MB)。
func maxUploadBytes() int64 {
	mb := 10
	if config.AppConfig != nil && config.AppConfig.Storage.MaxSizeMB > 0 {
		mb = config.AppConfig.Storage.MaxSizeMB
	}
	return int64(mb) << 20
}

// UploadFile 接收 multipart 文件并存储到本地，返回可访问 URL。
// POST /api/v1/upload  (form-data: file=<binary>)
func UploadFile(c *gin.Context) {
	userID := c.GetUint64("user_id")

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "缺少上传文件 (字段名 file)")
		return
	}

	// 大小校验
	if fileHeader.Size <= 0 || fileHeader.Size > maxUploadBytes() {
		response.Fail(c, 4400, fmt.Sprintf("文件大小超限 (最大 %dMB)", maxUploadBytes()>>20))
		return
	}

	// 类型校验：请求头 Content-Type 或文件扩展名任一命中白名单即可。
	// (multipart 常将部件 Content-Type 置为 application/octet-stream，需回退扩展名判断)
	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	headerCT := normalizeMIME(fileHeader.Header.Get("Content-Type"))
	extCT := normalizeMIME(mime.TypeByExtension(ext))

	contentType, normExt := "", ""
	if e, ok := allowedUploadTypes[headerCT]; ok {
		contentType, normExt = headerCT, e
	} else if e, ok := allowedUploadTypes[extCT]; ok {
		contentType, normExt = extCT, e
	} else {
		response.Fail(c, 4401, "不支持的文件类型，仅允许图片或音频")
		return
	}

	// 生成随机文件名，按用户分目录
	name, err := randomName()
	if err != nil {
		response.ServerError(c, "生成文件名失败")
		return
	}
	relDir := filepath.Join("uploads", "users", fmt.Sprintf("%d", userID))
	relPath := filepath.Join(relDir, name+normExt)
	absPath := filepath.Join(storageDir(), relPath)

	if err := ensureDir(filepath.Dir(absPath)); err != nil {
		response.ServerError(c, "存储目录创建失败")
		return
	}
	if err := c.SaveUploadedFile(fileHeader, absPath); err != nil {
		response.ServerError(c, "文件保存失败")
		return
	}

	// 组装对外 URL: {publicBaseURL}/files/{relPath}
	urlPath := "/files/" + filepath.ToSlash(relPath)
	fullURL := storagePublicBaseURL() + urlPath

	response.OK(c, gin.H{
		"url":  fullURL,
		"path": urlPath,
		"mime": contentType,
		"size": fileHeader.Size,
	})
}

// ServeFile 提供已上传文件的静态访问，含路径穿越防护。
// GET /files/*filepath
func ServeFile(c *gin.Context) {
	// gin 通配参数以 "/" 开头
	reqPath := strings.TrimPrefix(c.Param("filepath"), "/")
	// 清理并禁止穿越
	clean := path.Clean("/" + reqPath)
	if strings.Contains(clean, "..") {
		c.Status(http.StatusBadRequest)
		return
	}
	absPath := filepath.Join(storageDir(), filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	// 再次确认结果仍在存储根目录内
	root, _ := filepath.Abs(storageDir())
	target, _ := filepath.Abs(absPath)
	if !strings.HasPrefix(target, root) {
		c.Status(http.StatusForbidden)
		return
	}
	c.File(absPath)
}

// isValidMediaURL 校验媒体 URL 是否可信 (防止 SSRF / 任意外链)。
//
// 允许:
//   - 指向本服务自建存储 (配置的 public_base_url) 的 URL
//   - HTTPS 且域名在受信 CDN 白名单内 (兼容历史数据)
func isValidMediaURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	if base := storagePublicBaseURL(); base != "" && strings.HasPrefix(rawURL, base+"/files/") {
		return true
	}
	// 兼容: 允许受信 HTTPS CDN
	if strings.HasPrefix(rawURL, "https://") {
		trusted := []string{"aliyuncs.com/", "myqcloud.com/", "amazonaws.com/"}
		for _, t := range trusted {
			if strings.Contains(rawURL, t) {
				return true
			}
		}
	}
	return false
}

// ==================== 工具函数 ====================

func randomName() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// normalizeMIME 去除 MIME 参数并转小写, 如 "audio/mpeg; charset=..." -> "audio/mpeg"。
func normalizeMIME(ct string) string {
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}
