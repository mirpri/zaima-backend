// Package weather 基于 Open-Meteo (免费、无需 API Key) 实现天气查询。
//
// 流程: 城市名 -> Geocoding 拿经纬度 -> Forecast 拿实时天气 -> 映射 WMO 天气代码为中文。
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"zaima-backend/internal/config"
)

// Data 标准化的天气结果。
type Data struct {
	City     string `json:"city"`
	Temp     string `json:"temp"`     // 如 "15°C"
	TempNum  int    `json:"temp_num"` // 数值温度, 供逻辑判断
	Text     string `json:"text"`     // 中文天气描述, 如 "多云"
	Icon     string `json:"icon"`     // 图标标识
	WindDir  string `json:"wind_dir"` // 风向
	Humidity string `json:"humidity"` // 湿度
	Tips     string `json:"tips"`     // 关怀提示
	Updated  string `json:"updated"`
}

// ErrNotConfigured 表示未配置天气服务地址。
var ErrNotConfigured = errors.New("weather 未配置 base_url")

var httpClient = &http.Client{Timeout: 6 * time.Second}

type geoResp struct {
	Results []struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Name      string  `json:"name"`
	} `json:"results"`
}

type forecastResp struct {
	Current struct {
		Temperature   float64 `json:"temperature_2m"`
		Humidity      float64 `json:"relative_humidity_2m"`
		WeatherCode   int     `json:"weather_code"`
		WindDirection float64 `json:"wind_direction_10m"`
	} `json:"current"`
}

// Fetch 查询指定城市的实时天气。
func Fetch(ctx context.Context, city string) (*Data, error) {
	if config.AppConfig == nil || config.AppConfig.Weather.BaseURL == "" {
		return nil, ErrNotConfigured
	}
	cfg := config.AppConfig.Weather

	lat, lon, resolvedName, err := geocode(ctx, cfg.GeoURL, city)
	if err != nil {
		return nil, err
	}

	fURL := fmt.Sprintf(
		"%s/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,weather_code,wind_direction_10m&timezone=auto",
		strings.TrimRight(cfg.BaseURL, "/"), lat, lon,
	)
	var fr forecastResp
	if err := getJSON(ctx, fURL, &fr); err != nil {
		return nil, err
	}

	text, icon := describeWMO(fr.Current.WeatherCode)
	tempNum := int(fr.Current.Temperature + 0.5)
	name := resolvedName
	if name == "" {
		name = city
	}
	return &Data{
		City:     name,
		Temp:     fmt.Sprintf("%d°C", tempNum),
		TempNum:  tempNum,
		Text:     text,
		Icon:     icon,
		WindDir:  windDir(fr.Current.WindDirection),
		Humidity: fmt.Sprintf("%d%%", int(fr.Current.Humidity+0.5)),
		Tips:     tips(tempNum, fr.Current.WeatherCode),
		Updated:  time.Now().Format("2006-01-02 15:04"),
	}, nil
}

func geocode(ctx context.Context, geoURL, city string) (lat, lon float64, name string, err error) {
	if geoURL == "" {
		return 0, 0, "", ErrNotConfigured
	}
	// Open-Meteo 地理编码不认"市/省/区"等后缀，去掉后再查 (如 "武汉市" -> "武汉")
	query := normalizeCityName(city)
	u := fmt.Sprintf("%s/search?name=%s&count=1&language=zh&format=json",
		strings.TrimRight(geoURL, "/"), url.QueryEscape(query))
	var gr geoResp
	if err := getJSON(ctx, u, &gr); err != nil {
		return 0, 0, "", err
	}
	if len(gr.Results) == 0 {
		return 0, 0, "", fmt.Errorf("未找到城市: %s", city)
	}
	r := gr.Results[0]
	return r.Latitude, r.Longitude, r.Name, nil
}

// normalizeCityName 去除中文行政区划后缀，提升地理编码命中率。
func normalizeCityName(city string) string {
	city = strings.TrimSpace(city)
	suffixes := []string{"特别行政区", "自治区", "自治州", "地区", "省", "市", "区", "县", "盟"}
	for _, s := range suffixes {
		if strings.HasSuffix(city, s) && len([]rune(city)) > len([]rune(s)) {
			return strings.TrimSuffix(city, s)
		}
	}
	return city
}

func getJSON(ctx context.Context, u string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("天气接口状态码 %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// describeWMO 将 WMO 天气代码映射为中文描述与图标。
func describeWMO(code int) (text, icon string) {
	switch {
	case code == 0:
		return "晴", "sunny"
	case code >= 1 && code <= 3:
		return "多云", "cloudy"
	case code == 45 || code == 48:
		return "雾", "fog"
	case code >= 51 && code <= 57:
		return "毛毛雨", "drizzle"
	case code >= 61 && code <= 67:
		return "下雨", "rain"
	case code >= 71 && code <= 77:
		return "下雪", "snow"
	case code >= 80 && code <= 82:
		return "阵雨", "rain"
	case code >= 85 && code <= 86:
		return "阵雪", "snow"
	case code >= 95:
		return "雷阵雨", "thunder"
	default:
		return "多云", "cloudy"
	}
}

func windDir(deg float64) string {
	dirs := []string{"北风", "东北风", "东风", "东南风", "南风", "西南风", "西风", "西北风"}
	idx := int((deg+22.5)/45.0) % 8
	if idx < 0 {
		idx += 8
	}
	return dirs[idx]
}

func tips(temp, code int) string {
	if code >= 61 && code <= 82 {
		return "有雨，提醒记得带伞~"
	}
	if code >= 71 && code <= 86 {
		return "有雪路滑，出门注意保暖防摔~"
	}
	if temp <= 5 {
		return "天气很冷，记得多穿点衣服~"
	}
	if temp >= 32 {
		return "天气炎热，注意防暑多喝水~"
	}
	return "天气不错，适合出门走走~"
}
