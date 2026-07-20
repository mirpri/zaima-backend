// Package config 提供全局配置的加载与解析。
// 使用 Viper 读取 YAML 配置文件并映射到结构体。
package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/viper"
)

// AppConfig 全局配置实例，由 Load() 初始化。
var AppConfig *Config

// Config 应用总配置结构体。
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	JWT      JWTConfig      `mapstructure:"jwt"`
	SMS      SMSConfig      `mapstructure:"sms"`
	Storage  StorageConfig  `mapstructure:"storage"`
	LLM      LLMConfig      `mapstructure:"llm"`
	Weather  WeatherConfig  `mapstructure:"weather"`
	News     NewsConfig     `mapstructure:"news"`
	Push     PushConfig     `mapstructure:"push"`
}

// ServerConfig HTTP 服务器配置。
type ServerConfig struct {
	Port         int      `mapstructure:"port"`
	Mode         string   `mapstructure:"mode"` // debug / release / test
	AllowOrigins []string `mapstructure:"allow_origins"`
}

// DatabaseConfig PostgreSQL 连接配置。
type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	DBName       string `mapstructure:"dbname"`
	SSLMode      string `mapstructure:"sslmode"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

// DSN 返回 PostgreSQL 连接字符串。
func (d *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode,
	)
}

// RedisConfig Redis 连接配置。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// JWTConfig JWT 鉴权配置。
type JWTConfig struct {
	Secret      string `mapstructure:"secret"`
	ExpireHours int    `mapstructure:"expire_hours"`
}

// SMSConfig 短信服务配置。
type SMSConfig struct {
	Provider        string `mapstructure:"provider"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	SignName        string `mapstructure:"sign_name"`
	TemplateCode    string `mapstructure:"template_code"`
}

// StorageConfig 自建文件存储配置 (替代第三方 OSS)。
type StorageConfig struct {
	Dir           string `mapstructure:"dir"`             // 本地存储根目录, 如 ./data/uploads
	PublicBaseURL string `mapstructure:"public_base_url"` // 对外访问前缀, 如 https://zaima.example.com
	MaxSizeMB     int    `mapstructure:"max_size_mb"`     // 单文件大小上限 (MB)
}

// LLMConfig 大语言模型配置。
type LLMConfig struct {
	Provider       string `mapstructure:"provider"`
	APIKey         string `mapstructure:"api_key"`
	BaseURL        string `mapstructure:"base_url"`
	Model          string `mapstructure:"model"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

// WeatherConfig 天气配置 (Open-Meteo, 免费无需 key)。
type WeatherConfig struct {
	BaseURL     string `mapstructure:"base_url"` // 预报接口, 如 https://api.open-meteo.com/v1
	GeoURL      string `mapstructure:"geo_url"`  // 地理编码接口, 如 https://geocoding-api.open-meteo.com/v1
	CacheTTLHrs int    `mapstructure:"cache_ttl_hours"`
}

// NewsConfig 新闻配置 (自建 RSS 抓取)。
type NewsConfig struct {
	Feeds       []string `mapstructure:"feeds"` // RSS/Atom 源列表
	CacheTTLHrs int      `mapstructure:"cache_ttl_hours"`
}

// PushConfig 推送服务配置。
type PushConfig struct {
	AppKey       string `mapstructure:"app_key"`
	MasterSecret string `mapstructure:"master_secret"`
}

// Load 加载指定路径下的配置文件到全局变量 AppConfig。
func Load(path string) {
	viper.SetConfigFile(path)

	// 【安全】支持环境变量覆盖敏感配置 (如 ZAIMA_JWT_SECRET, ZAIMA_DATABASE_PASSWORD)
	viper.SetEnvPrefix("ZAIMA")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("[config] 读取配置文件失败: %v", err)
	}

	AppConfig = &Config{}
	if err := viper.Unmarshal(AppConfig); err != nil {
		log.Fatalf("[config] 解析配置文件失败: %v", err)
	}

	log.Printf("[config] 配置加载成功: server.port=%d, mode=%s",
		AppConfig.Server.Port, AppConfig.Server.Mode)
}
