package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 对应 config.yaml 的根结构
type Config struct {
	Sync   SyncConfig   `yaml:"sync"`
	Baidu  BaiduConfig  `yaml:"baidu"`
	Crypto CryptoConfig `yaml:"crypto"`
	System SystemConfig `yaml:"system"`
}

// SyncConfig 同步相关配置
type SyncConfig struct {
	LocalDir      string `yaml:"local_dir"`
	RemoteDir     string `yaml:"remote_dir"`
	Interval      string `yaml:"interval"`
	MaxConcurrent int    `yaml:"max_concurrent"`
	// rename_local (默认): 重命名本地文件
	// rename_remote: 重命名云端文件
	// keep_latest: 保留时间最新的文件
	// delete_remote: 删除云端文件 (强制以本地为准)
	// delete_local: 删除本地文件 (强制以云端为准)
	ConflictStrategy string `yaml:"conflict_strategy"`
	// 也就是解析后的 duration，不导出到 yaml
	IntervalDuration time.Duration `yaml:"-"`
}

// BaiduConfig 百度网盘 API 配置
type BaiduConfig struct {
	AppKey       string `yaml:"app_key"`
	SecretKey    string `yaml:"secret_key"`
	AccessToken  string `yaml:"access_token"`
	RefreshToken string `yaml:"refresh_token"`
	UserAgent    string `yaml:"user_agent"`
}

// CryptoConfig 加密配置
type CryptoConfig struct {
	Enable           bool   `yaml:"enable"`
	Password         string `yaml:"password"`
	EncryptFilenames bool   `yaml:"encrypt_filenames"`
	Algorithm        string `yaml:"algorithm"`
}

// SystemConfig 系统配置
type SystemConfig struct {
	DBPath   string `yaml:"db_path"`
	TempDir  string `yaml:"temp_dir"`
	LogLevel string `yaml:"log_level"`
	LogFile  string `yaml:"log_file"`
}

// LoadConfig 读取并解析配置文件
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 YAML 格式错误: %w", err)
	}

	// 校验与转换
	// ... (IntervalDuration 解析保持不变) ...
	duration, err := time.ParseDuration(cfg.Sync.Interval)
	if err != nil {
		return nil, fmt.Errorf("无效的同步间隔格式 (sync.interval): %v", err)
	}
	cfg.Sync.IntervalDuration = duration

	// 设置默认冲突策略
	if cfg.Sync.ConflictStrategy == "" {
		cfg.Sync.ConflictStrategy = "rename_local"
	}
	// 简单校验策略合法性
	validStrategies := map[string]bool{
		"rename_local": true, "rename_remote": true,
		"keep_latest": true, "delete_remote": true, "delete_local": true,
	}
	if !validStrategies[cfg.Sync.ConflictStrategy] {
		return nil, fmt.Errorf("未知的冲突策略: %s", cfg.Sync.ConflictStrategy)
	}

	// 设置默认临时目录
	if cfg.System.TempDir == "" {
		cfg.System.TempDir = "./tmp"
	}

	// 确保临时目录存在
	if err := os.MkdirAll(cfg.System.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("无法创建临时目录: %w", err)
	}

	return &cfg, nil
}

// GetAESKey 将用户输入的任意长度密码转换为 32字节 的 AES-256 密钥
// 使用 SHA-256 哈希算法
func (c *CryptoConfig) GetAESKey() []byte {
	hash := sha256.Sum256([]byte(c.Password))
	return hash[:] // 返回切片 [32]byte -> []byte
}
