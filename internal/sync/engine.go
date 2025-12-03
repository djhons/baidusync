package sync

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"baidusync/internal/crypto"
	"baidusync/internal/database"
	"baidusync/internal/fs"
	"golang.org/x/sync/errgroup"
)

type ConflictStrategy int

const (
	// StrategyRenameLocal (默认/0)：重命名本地为 .local，下载云端版本
	StrategyRenameLocal ConflictStrategy = iota
	// StrategyRenameRemote (1)：重命名云端为 .remote，上传本地版本
	StrategyRenameRemote
	// StrategyKeepNewest (2)：保留时间较新的版本
	StrategyKeepNewest
	// StrategyForceUpload (3)：删除云端，强制上传本地 (对应 config: delete_remote)
	StrategyForceUpload
	// StrategyForceDownload (4)：删除本地，强制下载云端 (对应 config: delete_local)
	StrategyForceDownload
)

// ParseConflictStrategy 将配置文件中的字符串转换为引擎内部的枚举值
func ParseConflictStrategy(s string) ConflictStrategy {
	switch s {
	case "rename_remote":
		return StrategyRenameRemote
	case "keep_latest":
		return StrategyKeepNewest
	case "delete_remote":
		// 配置叫“删除云端”，实际操作逻辑是“强制上传(覆盖)”
		return StrategyForceUpload
	case "delete_local":
		// 配置叫“删除本地”，实际操作逻辑是“强制下载(覆盖)”
		return StrategyForceDownload
	default:
		// 默认 "rename_local" 或其他未知值
		return StrategyRenameLocal
	}
}

// EngineOptions 初始化选项
type EngineOptions struct {
	LocalFS          fs.FileSystem
	RemoteFS         fs.FileSystem
	StateDB          *database.DB
	EncryptKey       []byte // 32字节密钥
	EncryptFilenames bool   // 是否加密文件名
	MaxWorkers       int
	ConflictStrategy ConflictStrategy
}

type Engine struct {
	opts *EngineOptions
}

func NewEngine(opts *EngineOptions) *Engine {
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 3
	}
	return &Engine{opts: opts}
}

// Run 执行一次完整的同步周期
func (e *Engine) Run(ctx context.Context) error {
	// 1. 获取三方状态 (并发获取以加速)
	var (
		localMap  map[string]*fs.FileMeta
		remoteMap map[string]*fs.FileMeta
		baseMap   map[string]*database.FileState
	)

	g, _ := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		localMap, err = e.opts.LocalFS.ListAll()
		if err != nil {
			return fmt.Errorf("scan local failed: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		var err error
		remoteMap, err = e.opts.RemoteFS.ListAll()
		if err != nil {
			return fmt.Errorf("scan remote failed: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		var err error
		baseMap, err = e.opts.StateDB.ListAll()
		if err != nil {
			return fmt.Errorf("scan db failed: %w", err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// 2. 生成任务队列
	tasks := make([]Task, 0)

	// 收集所有出现过的路径 (并集)
	allPaths := make(map[string]bool)
	for p := range localMap {
		allPaths[p] = true
	}
	for p := range remoteMap {
		allPaths[p] = true
	}
	for p := range baseMap {
		allPaths[p] = true
	}

	for path := range allPaths {
		l := localMap[path]
		r := remoteMap[path]
		b := baseMap[path]

		// 调用 diff.go 中的 compare 逻辑
		op := e.compare(path, l, r, b)

		if op != OpIgnore {
			tasks = append(tasks, Task{Op: op, RelPath: path})
		} else {
			// 【关键逻辑】静默重建索引
			// 如果 compare 返回 Ignore，说明两边一致。
			// 但如果 baseMap 中没有记录 (b==nil)，说明是 DB 丢失后的首次模糊匹配成功。
			// 此时需要立即写入一条记录，建立关联，否则下次比对缺乏基准。
			if b == nil && l != nil && r != nil {
				e.rebuildIndex(path, l, r)
			}
		}
	}

	slog.Info(
		"同步检查完成",
		"发现任务数", len(tasks),
	)
	if len(tasks) == 0 {
		return nil
	}

	// 3. 启动 Worker 池执行任务
	taskChan := make(chan Task, len(tasks))
	for _, t := range tasks {
		taskChan <- t
	}
	close(taskChan)

	var wg sync.WaitGroup
	// 简单的错误收集
	errChan := make(chan error, len(tasks))

	for i := 0; i < e.opts.MaxWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for task := range taskChan {
				// 检查上下文是否取消
				select {
				case <-ctx.Done():
					return
				default:
				}

				if err := e.processTask(ctx, task); err != nil {
					slog.Error("[Worker] 任务失败",
						"worker", id,
						"path", task.RelPath,
						"op", task.Op,
						"err", err,
					)
					errChan <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// 收集所有错误
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		// 将多个错误合并为一个
		return fmt.Errorf("%d task(s) failed: %v", len(errs), errs)
	}

	return nil
}

// rebuildIndex 静默重建索引（不传输文件）
func (e *Engine) rebuildIndex(path string, l, r *fs.FileMeta) {
	// 构造新的状态记录
	newState := &database.FileState{
		RelPath:  path,
		FileSize: l.Size,               // 以本地明文大小为准
		ModTime:  l.ModTime.UnixNano(), // 以本地时间为准

		// 关键：重建索引时，我们认为两边内容一致，所以都用本地明文Hash作为基准
		LocalHash:    l.Hash,
		RemoteHash:   l.Hash, // <--- 使用本地Hash
		LastSyncTime: time.Now().Unix(),
	}

	slog.Info("DB丢失恢复: 重新关联文件", "path", path)

	// 写入数据库
	if err := e.opts.StateDB.Put(newState); err != nil {
		slog.Error("重建索引失败", "path", path, "err", err)
	}
}

// processTask 处理单个任务
func (e *Engine) processTask(ctx context.Context, t Task) error {
	switch t.Op {
	case OpUpload:
		return e.doUpload(t.RelPath)
	case OpDownload:
		return e.doDownload(t.RelPath)
	case OpDeleteRemote:
		if err := e.opts.RemoteFS.Delete(t.RelPath); err != nil {
			return err
		}
		return e.opts.StateDB.Delete(t.RelPath)
	case OpDeleteLocal:
		if err := e.opts.LocalFS.Delete(t.RelPath); err != nil {
			return err
		}
		return e.opts.StateDB.Delete(t.RelPath)
	case OpConflict:
		// 修改：调用专门的冲突处理逻辑
		return e.resolveConflict(ctx, t.RelPath)
	}
	return nil
}
func (e *Engine) resolveConflict(ctx context.Context, path string) error {
	strategy := e.opts.ConflictStrategy
	slog.Info("开始解决冲突", "path", path, "strategy", strategy)

	switch strategy {
	case StrategyRenameLocal:
		// 选项一：本地重命名为 .local，然后下载云端文件
		newName := path + ".local"
		slog.Info("冲突处理: 重命名本地文件", "old", path, "new", newName)

		// 1. 重命名本地文件
		if err := e.opts.LocalFS.Rename(path, newName); err != nil {
			return fmt.Errorf("rename local failed: %w", err)
		}
		// 2. 原路径现在空了，执行下载
		return e.doDownload(path)

	case StrategyRenameRemote:
		// 选项二：云端重命名为 .remote，然后上传本地文件
		newName := path + ".remote"
		slog.Info("冲突处理: 重命名云端文件", "old", path, "new", newName)

		// 1. 重命名云端文件
		if err := e.opts.RemoteFS.Rename(path, newName); err != nil {
			return fmt.Errorf("rename remote failed: %w", err)
		}
		// 2. 原路径云端文件已移走，执行上传
		return e.doUpload(path)

	case StrategyKeepNewest:
		// 选项三：比较时间，保留新的
		localMeta, err := e.opts.LocalFS.Stat(path)
		if err != nil {
			return fmt.Errorf("stat local failed: %w", err)
		}
		remoteMeta, err := e.opts.RemoteFS.Stat(path)
		if err != nil {
			return fmt.Errorf("stat remote failed: %w", err)
		}

		slog.Info("冲突处理: 时间比对",
			"localTime", localMeta.ModTime,
			"remoteTime", remoteMeta.ModTime)

		if localMeta.ModTime.After(remoteMeta.ModTime) {
			// 本地更新 -> 上传（覆盖云端）
			slog.Info("本地文件较新，执行上传覆盖")
			return e.doUpload(path)
		} else {
			// 云端更新(或相等) -> 下载（覆盖本地）
			slog.Info("云端文件较新，执行下载覆盖")
			return e.doDownload(path)
		}

	case StrategyForceUpload:
		// 选项四：删除云端，上传本地
		slog.Info("冲突处理: 强制删除云端并上传")
		// 先删除云端文件，确保写入时是个新文件（有些网盘覆盖逻辑复杂，删除更稳妥）
		if err := e.opts.RemoteFS.Delete(path); err != nil {
			return fmt.Errorf("delete remote failed: %w", err)
		}
		return e.doUpload(path)

	case StrategyForceDownload:
		// 选项五：删除本地，下载云端
		slog.Info("冲突处理: 强制删除本地并下载")
		if err := e.opts.LocalFS.Delete(path); err != nil {
			return fmt.Errorf("delete local failed: %w", err)
		}
		return e.doDownload(path)

	default:
		// 默认行为（防止配置错误）
		slog.Warn("未知的冲突策略，跳过处理", "strategy", strategy)
		return nil
	}
}

// doUpload 上传流程：读取本地 -> 加密 -> 写入网盘 -> 更新DB
func (e *Engine) doUpload(path string) error {
	slog.Info("开始上传", "path", path)

	// 1. 打开本地流
	reader, err := e.opts.LocalFS.OpenStream(path)
	if err != nil {
		return err
	}
	defer reader.Close()

	// 2. 包装加密流 (Crypto Stream)
	var uploadStream io.Reader = reader
	if len(e.opts.EncryptKey) > 0 {
		encryptedReader, err := crypto.NewEncryptReader(reader, e.opts.EncryptKey)
		if err != nil {
			return fmt.Errorf("crypto init failed: %w", err)
		}
		uploadStream = encryptedReader
	}

	// 3. 传输到网盘 (返回云端密文 MD5)
	// RemoteFS.WriteStream 必须返回 (cloudMD5, error)
	cloudMD5, err := e.opts.RemoteFS.WriteStream(path, uploadStream, time.Now())
	if err != nil {
		return err
	}

	// 4. 上传成功，更新数据库状态
	// 需要重新 Stat 获取本地最新状态，作为基准 (必须包含 LocalHash)
	stat, err := e.opts.LocalFS.Stat(path)
	if err != nil {
		return fmt.Errorf("stat local failed after upload: %w", err)
	}

	newState := &database.FileState{
		RelPath:      path,
		FileSize:     stat.Size,               // 本地文件大小
		ModTime:      stat.ModTime.UnixNano(), // 本地修改时间
		LocalHash:    stat.Hash,               // 【重要】本地明文 Hash (由 LocalFS.Stat 计算)
		RemoteHash:   cloudMD5,                // 【重要】云端密文 Hash (由 WriteStream 返回)
		LastSyncTime: time.Now().Unix(),
	}

	slog.Debug("更新数据库状态(Upload)",
		"path", path,
		"localHash", newState.LocalHash,
		"remoteHash", newState.RemoteHash)

	return e.opts.StateDB.Put(newState)
}

// doDownload 下载流程：读取网盘 -> 解密 -> 写入本地 -> 更新DB
func (e *Engine) doDownload(path string) error {
	slog.Info("开始下载任务", "path", path)

	// 1. 打开网盘流
	reader, err := e.opts.RemoteFS.OpenStream(path)
	if err != nil {
		return err
	}
	defer reader.Close()

	// 2. 包装解密流
	var downStream io.Reader = reader
	if len(e.opts.EncryptKey) > 0 {
		decryptedReader, err := crypto.NewDecryptReader(reader, e.opts.EncryptKey)
		if err != nil {
			return fmt.Errorf("crypto init failed: %w", err)
		}
		downStream = decryptedReader
	}

	// 3. 获取云端元数据 (为了恢复 MTime 和获取 RemoteHash)
	remoteMeta, err := e.opts.RemoteFS.Stat(path)
	if err != nil {
		return err
	}

	// 4. 写入本地 (返回本地计算的明文 MD5)
	// LocalFS.WriteStream 必须返回 (localMD5, error)
	localMD5, err := e.opts.LocalFS.WriteStream(path, downStream, remoteMeta.ModTime)
	if err != nil {
		return err
	}

	// 5. 更新数据库
	// 重新获取本地状态确保一致
	localStat, err := e.opts.LocalFS.Stat(path)
	if err != nil {
		return fmt.Errorf("stat local after download failed: %w", err)
	}

	newState := &database.FileState{
		RelPath:      path,
		FileSize:     localStat.Size,
		ModTime:      localStat.ModTime.UnixNano(),
		LocalHash:    localMD5,              // 【重要】本地明文 Hash
		RemoteHash:   remoteMeta.RemoteHash, // 【重要】云端密文 Hash
		LastSyncTime: time.Now().Unix(),
	}

	slog.Debug("更新数据库(Download)",
		"path", path,
		"localHash", newState.LocalHash,
		"remoteHash", newState.RemoteHash)

	return e.opts.StateDB.Put(newState)
}
