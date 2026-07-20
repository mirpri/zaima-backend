// Package news 实现自建的 RSS/Atom 新闻抓取 (替代第三方新闻 API)。
package news

import (
	"context"
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"zaima-backend/internal/config"
	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// rssFeed 兼容 RSS 2.0 结构。
type rssFeed struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

// FetchAll 抓取配置中的所有 RSS 源并写入 news_cache，返回新增条数。
func FetchAll(ctx context.Context) int {
	if config.AppConfig == nil || len(config.AppConfig.News.Feeds) == 0 {
		return 0
	}
	total := 0
	for _, feed := range config.AppConfig.News.Feeds {
		n, err := fetchFeed(ctx, feed)
		if err != nil {
			log.Printf("[news] 抓取失败 %s: %v", feed, err)
			continue
		}
		total += n
	}
	if total > 0 {
		log.Printf("[news] 本次新增 %d 条新闻", total)
	}
	return total
}

func fetchFeed(ctx context.Context, feedURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 最多 4MB
	if err != nil {
		return 0, err
	}

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return 0, err
	}

	inserted := 0
	for _, item := range feed.Channel.Items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			continue
		}
		// 去重: 同标题已存在则跳过
		var exist int64
		database.DB.Model(&model.NewsCache{}).Where("title = ?", title).Count(&exist)
		if exist > 0 {
			continue
		}
		record := model.NewsCache{
			City:      "全国",
			Category:  "all",
			Title:     title,
			Summary:   stripHTML(item.Description),
			SourceURL: strings.TrimSpace(item.Link),
			PubDate:   parsePubDate(item.PubDate),
		}
		if err := database.DB.Create(&record).Error; err == nil {
			inserted++
		}
	}
	return inserted, nil
}

// stripHTML 粗略去除摘要中的 HTML 标签并截断。
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len([]rune(out)) > 120 {
		out = string([]rune(out)[:120]) + "…"
	}
	return out
}

// parsePubDate 尝试解析常见 RSS 时间格式，返回 YYYY-MM-DD。
func parsePubDate(s string) string {
	s = strings.TrimSpace(s)
	layouts := []string{time.RFC1123Z, time.RFC1123, time.RFC3339}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return time.Now().Format("2006-01-02")
}
