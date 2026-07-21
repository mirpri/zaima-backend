// Package handler - 天气与新闻查询 (带缓存与搜索)。
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"zaima-backend/internal/config"
	"zaima-backend/internal/model"
	"zaima-backend/internal/pkg/database"
	newsfetch "zaima-backend/internal/pkg/news"
	"zaima-backend/internal/pkg/response"
	"zaima-backend/internal/pkg/weather"
)

// ==================== Handler ====================

// GetWeather 获取子女所在城市的天气信息。
// GET /api/v1/weather?city=武汉
//
// 先从 Redis 缓存读取，失效后调用第三方 API 刷新。
func GetWeather(c *gin.Context) {
	city := c.Query("city")
	if city == "" {
		response.BadRequest(c, "缺少 city 参数")
		return
	}

	ctx := context.Background()
	cacheKey := fmt.Sprintf("weather:%s", city)

	// 1. 尝试读取 Redis 缓存
	cached, err := database.RDB.Get(ctx, cacheKey).Result()
	if err == nil && cached != "" {
		// 【修复】反序列化为 JSON 对象后返回
		var cachedData map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(cached), &cachedData); jsonErr == nil {
			response.OK(c, gin.H{
				"source": "cache",
				"data":   cachedData,
			})
			return
		}
	}

	// 2. 缓存未命中，调用 Open-Meteo 实时查询
	source := "api"
	var weatherData interface{}
	if wd, err := weather.Fetch(c.Request.Context(), city); err == nil {
		weatherData = wd
		// 命中真实数据才写缓存
		ttl := time.Duration(config.AppConfig.Weather.CacheTTLHrs) * time.Hour
		if ttl == 0 {
			ttl = 12 * time.Hour
		}
		jsonBytes, _ := json.Marshal(wd)
		database.RDB.Set(ctx, cacheKey, string(jsonBytes), ttl)
	} else {
		// 天气服务不可用时的兜底 (不缓存)
		source = "fallback"
		weatherData = gin.H{
			"city":    city,
			"temp":    "--",
			"text":    "暂无数据",
			"icon":    "cloudy",
			"tips":    "天气服务暂时不可用，请稍后再看~",
			"updated": time.Now().Format("2006-01-02 15:04"),
		}
	}

	response.OK(c, gin.H{
		"source": source,
		"data":   weatherData,
	})
}

// GetCareCards 获取 AI 关怀气泡 (根据天气和时间段)。
// GET /api/v1/weather/care-cards?city=武汉
func GetCareCards(c *gin.Context) {
	// 根据时间段 + 天气条件生成关怀卡片文案
	now := time.Now()
	hour := now.Hour()

	cards := []gin.H{}

	// 根据时间段生成不同建议
	if hour >= 6 && hour < 9 {
		cards = append(cards, gin.H{"icon": "sunrise", "text": "早上好，记得吃早餐哦~"})
	}
	if hour >= 11 && hour < 14 {
		cards = append(cards, gin.H{"icon": "bowl", "text": "工作再忙也要按时吃饭~"})
	}
	if hour >= 22 || hour < 5 {
		cards = append(cards, gin.H{"icon": "moon", "text": "早点休息，别熬夜~"})
	}

	// 结合实时天气动态追加关怀卡片 (降温/下雨等)
	if city := c.Query("city"); city != "" {
		if wd, err := weather.Fetch(c.Request.Context(), city); err == nil && wd.Tips != "" {
			cards = append(cards, gin.H{"icon": wd.Icon, "text": wd.Tips})
		}
	}

	cards = append(cards, gin.H{"icon": "heart", "text": "想你了，有空打个电话~"})

	response.OK(c, gin.H{"cards": cards})
}

// GetNews 获取孩子所在城市的新闻列表。
// GET /api/v1/news?city=武汉&tab=all&keyword=xxx&page=1&page_size=10
//
// 支持 tab 分类 (all/latest/hot) 和关键字搜索，带半天 Redis 缓存。
func GetNews(c *gin.Context) {
	city := c.DefaultQuery("city", "")
	tab := c.DefaultQuery("tab", "all") // all / latest / hot
	keyword := c.DefaultQuery("keyword", "")
	page := c.DefaultQuery("page", "1")
	pageSize := c.DefaultQuery("page_size", "10")

	if city == "" {
		// 兜底：返回全国热门新闻
		city = "全国"
	}

	ctx := context.Background()
	cacheKey := fmt.Sprintf("news:%s:%s", city, tab)

	// 1. 尝试读取 Redis 缓存 (无关键字搜索时)
	if keyword == "" {
		cached, err := database.RDB.Get(ctx, cacheKey).Result()
		if err == nil && cached != "" {
			response.OK(c, gin.H{
				"source": "cache",
				"city":   city,
				"tab":    tab,
				"data":   cached,
			})
			return
		}
	}

	// 2. 从数据库查询新闻：匹配所在城市，同时始终包含"全国"新闻
	//    (RSS 抓取的新闻按"全国"入库，适用于所有用户)
	query := database.DB.Model(&model.NewsCache{}).Where("city = ? OR city = ?", city, "全国")

	if tab == "latest" {
		query = query.Order("pub_date DESC")
	} else {
		query = query.Order("created_at DESC") // 默认/热点按抓取时间倒序
	}

	if keyword != "" {
		query = query.Where("title LIKE ?", "%"+keyword+"%")
	}

	// 【修复】启用支付分页参数
	pageNum, _ := strconv.Atoi(page)
	pageSz, _ := strconv.Atoi(pageSize)
	if pageNum < 1 {
		pageNum = 1
	}
	if pageSz < 1 || pageSz > 50 {
		pageSz = 10
	}

	var news []model.NewsCache
	query.Offset((pageNum - 1) * pageSz).Limit(pageSz).Find(&news)

	if len(news) == 0 {
		// 库里还没有新闻：异步触发一次 RSS 抓取（带 Redis 锁防并发），下次刷新即可看到
		if locked, _ := database.RDB.SetNX(ctx, "news:fetching", "1", 2*time.Minute).Result(); locked {
			go newsfetch.FetchAll(context.Background())
		}
		response.OK(c, gin.H{
			"city":    city,
			"tab":     tab,
			"data":    []interface{}{},
			"message": "正在为您抓取最新资讯，请稍后下拉刷新~",
		})
		return
	}

	response.OK(c, gin.H{
		"city":  city,
		"tab":   tab,
		"total": len(news),
		"data":  news,
	})
}
