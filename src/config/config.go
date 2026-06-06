package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	DB       DBConfig       `yaml:"db"`
	Log      LogConfig      `yaml:"log"`
	App      AppConfig      `yaml:"app"`
	Telegram TelegramConfig `yaml:"telegram"`
}

// LogConfig 日志配置
type LogConfig struct {
	Dir        string `yaml:"dir"`          // 日志目录
	MaxAgeDays int    `yaml:"max_age_days"` // 保留天数
	Level      string `yaml:"level"`        // debug | info | warn | error
}

// DBConfig 数据库配置
type DBConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	User            string `yaml:"user"`
	Password        string `yaml:"password"`
	Database        string `yaml:"database"`
	MaxOpenConns    int    `yaml:"max_open_conns"`
	MaxIdleConns    int    `yaml:"max_idle_conns"`
	ConnMaxLifetime int    `yaml:"conn_max_lifetime_secs"` // 秒
}

func (d DBConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		d.User, d.Password, d.Host, d.Port, d.Database)
}

// AppConfig 应用配置
type AppConfig struct {
	ShutdownTimeoutSecs int `yaml:"shutdown_timeout_secs"`
}

// TelegramConfig Telegram 通知配置
type TelegramConfig struct {
	Enabled             bool   `yaml:"enabled"`
	BotToken            string `yaml:"bot_token"`
	ChatID              string `yaml:"chat_id"`
	RPCFailureThreshold int    `yaml:"rpc_failure_threshold"`
	CooldownSecs        int    `yaml:"cooldown_secs"`
}

// defaultConfig 返回内置默认值
func defaultConfig() *Config {
	return &Config{
		DB: DBConfig{
			Host:            "127.0.0.1",
			Port:            3306,
			User:            "root",
			Password:        "666888",
			Database:        "paopao",
			MaxOpenConns:    25,
			MaxIdleConns:    10,
			ConnMaxLifetime: 300, // 5 分钟
		},
		Log: LogConfig{
			Dir:        "logs",
			MaxAgeDays: 30,
			Level:      "info",
		},
		App: AppConfig{
			ShutdownTimeoutSecs: 30,
		},
		Telegram: TelegramConfig{
			Enabled:             false,
			BotToken:            "",
			ChatID:              "",
			RPCFailureThreshold: 5,
			CooldownSecs:        1800,
		},
	}
}

// Load 加载配置：默认值 → config.yaml → 环境变量覆盖
func Load(configPath string) (*Config, error) {
	cfg := defaultConfig()

	// 1. 加载 YAML 文件（可选）
	if configPath == "" {
		configPath = "config.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config file %s: %w", configPath, err)
		}
		// 文件不存在，使用默认值 + 环境变量
		fmt.Fprintf(os.Stderr, "[config] %s not found, using defaults and env vars\n", configPath)
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config file %s: %w", configPath, err)
		}
	}

	// 2. 环境变量覆盖
	applyEnvOverrides(cfg)

	return cfg, nil
}

// applyEnvOverrides 用环境变量覆盖配置
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.DB.Host = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DB.Port = n
		}
	}
	if v := os.Getenv("DB_USER"); v != "" {
		cfg.DB.User = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.DB.Password = v
	}
	if v := os.Getenv("DB_DATABASE"); v != "" {
		cfg.DB.Database = v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("SHUTDOWN_TIMEOUT_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.App.ShutdownTimeoutSecs = n
		}
	}
	if v := os.Getenv("TELEGRAM_ENABLED"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.Telegram.Enabled = b
		}
	}
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv("TELEGRAM_CHAT_ID"); v != "" {
		cfg.Telegram.ChatID = v
	}
	if v := os.Getenv("TELEGRAM_RPC_FAILURE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Telegram.RPCFailureThreshold = n
		}
	}
	if v := os.Getenv("TELEGRAM_COOLDOWN_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Telegram.CooldownSecs = n
		}
	}
}

func parseBoolEnv(value string) (bool, bool) {
	switch value {
	case "1", "t", "T", "true", "TRUE", "True", "yes", "YES", "Yes", "y", "Y", "on", "ON", "On":
		return true, true
	case "0", "f", "F", "false", "FALSE", "False", "no", "NO", "No", "n", "N", "off", "OFF", "Off":
		return false, true
	default:
		return false, false
	}
}
