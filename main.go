package main

import (
	"baidusync/internal/config"
	"baidusync/internal/database"
	"baidusync/internal/fs/baidu"
	"baidusync/internal/fs/local"
	syncer "baidusync/internal/sync"
	"baidusync/pkg/logger"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {

	// 1. 加载配置
	configPath := "config/config.yaml"
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		panic("配置加载失败: " + err.Error())
	}

	// 2. 【关键】初始化日志系统
	if err := logger.Setup(cfg.System.LogLevel, cfg.System.LogFile); err != nil {
		panic("日志初始化失败: " + err.Error())
	}

	// 3. 开始使用
	slog.Info("BaiduSync 启动中",
		"version", "1.0.0",
		"log_level", cfg.System.LogLevel,
		"log_file", cfg.System.LogFile,
	)
	slog.Info("配置已加载",
		"local_dir", cfg.Sync.LocalDir,
		"remote_dir", cfg.Sync.RemoteDir,
		"interval", cfg.Sync.Interval,
	)
	// 3. 初始化数据库
	db, err := database.NewBoltDB(cfg.System.DBPath)
	if err != nil {
		slog.Error("无法打开数据库", "err", err, "path", cfg.System.DBPath)
		panic("数据库初始化失败: " + err.Error())
	}
	defer db.Close()

	// 4. 初始化文件适配器
	localFS := local.NewAdapter(cfg.Sync.LocalDir)

	// 初始化百度客户端 (传入更多认证信息)
	baiduClient := baidu.NewClient(&baidu.Options{
		AppKey:       cfg.Baidu.AppKey,
		SecretKey:    cfg.Baidu.SecretKey,
		AccessToken:  cfg.Baidu.AccessToken,
		RefreshToken: cfg.Baidu.RefreshToken,
		UserAgent:    cfg.Baidu.UserAgent,
	})

	// 5. 准备加密密钥
	var aesKey []byte
	if cfg.Crypto.Enable {
		aesKey = cfg.Crypto.GetAESKey() // 自动将密码转为32字节Key
		slog.Info("加密模式: 已启用 (AES-256)", "encrypt_filenames", cfg.Crypto.EncryptFilenames)
	} else {
		slog.Info("加密模式: 未启用 (文件将原样上传)")
	}

	// 传递加密参数到 Baidu Adapter
	baiduFS := baidu.NewAdapter(baiduClient, cfg.Sync.RemoteDir, aesKey, cfg.Crypto.EncryptFilenames)

	// 6. 初始化同步引擎
	engine := syncer.NewEngine(&syncer.EngineOptions{
		LocalFS:          localFS,
		RemoteFS:         baiduFS,
		StateDB:          db,
		EncryptKey:       aesKey,
		EncryptFilenames: cfg.Crypto.EncryptFilenames,
		MaxWorkers:       cfg.Sync.MaxConcurrent,
		ConflictStrategy: syncer.ParseConflictStrategy(cfg.Sync.ConflictStrategy),
	})

	// 7. 设置优雅退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	var isSyncing atomic.Bool

	runSync := func(appCtx context.Context) {
		if !isSyncing.CompareAndSwap(false, true) {
			slog.Info("上一轮同步尚未结束，跳过本次触发")
			return
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer isSyncing.Store(false)

			slog.Info(">>> 开始同步")
			if err := engine.Run(appCtx); err != nil {
				// 区分是外部取消还是真正的同步错误
				if appCtx.Err() != nil {
					slog.Warn("同步被中断")
				} else {
					slog.Error("同步错误", "error", err)
				}
			}
			slog.Info("<<< 同步结束")
		}()
	}

	// 立即运行一次
	runSync(ctx)

	// 主循环
	ticker := time.NewTicker(cfg.Sync.IntervalDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			runSync(ctx)
		case sig := <-sigChan:
			slog.Info("接收到信号，准备优雅退出...", "signal", sig)
			cancel()    // 通知所有 goroutine 退出
			wg.Wait()   // 等待所有同步任务完成
			slog.Info("所有任务已完成，程序退出")
			return
		case <-ctx.Done():
			// 如果是其他原因导致 ctx a被取消
			slog.Info("主上下文被取消，程序退出")
			return
		}
	}
}
