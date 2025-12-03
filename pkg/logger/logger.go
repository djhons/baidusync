package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Setup 初始化全局日志配置
// levelStr: "debug", "info", "warn", "error"
// logPath: 日志文件路径 (如果为空则只输出到控制台)
func Setup(levelStr string, logPath string) error {
	// 1. 解析日志等级
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// 2. 配置输出目标 (Writer)
	var writer io.Writer = os.Stdout

	if logPath != "" {
		// 确保日志目录存在
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
			return err
		}

		// 打开日志文件 (追加模式)
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return err
		}

		// 使用 MultiWriter 同时输出到控制台和文件
		writer = io.MultiWriter(os.Stdout, file)
	}

	// 3. 配置 Handler 选项
	opts := &slog.HandlerOptions{
		Level:     level,                    // 设置最低日志等级
		AddSource: level == slog.LevelDebug, // 仅在 Debug 模式下显示文件名和行号
		// 自定义时间格式等可以在这里通过 ReplaceAttr 处理
	}

	// 4. 创建 Logger (使用 TextHandler 方便人类阅读，也可以选 JSONHandler)
	logger := slog.New(slog.NewTextHandler(writer, opts))

	// 5. 设置为全局默认 Logger
	slog.SetDefault(logger)

	return nil
}
