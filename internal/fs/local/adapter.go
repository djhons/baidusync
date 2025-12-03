package local

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"baidusync/internal/fs"
	"log/slog"
)

// Adapter 本地文件系统适配器
type Adapter struct {
	rootDir string // 本地绝对路径根目录
}

// NewAdapter 创建一个新的本地适配器
func NewAdapter(rootDir string) *Adapter {
	// 确保 rootDir 是绝对路径
	absDir, err := filepath.Abs(rootDir)
	if err != nil {
		absDir = rootDir
	}
	return &Adapter{rootDir: absDir}
}

// Root 返回根目录
func (a *Adapter) Root() string {
	return a.rootDir
}

// toSysPath 将相对路径转换为本地系统绝对路径
// 输入: "docs/file.txt" -> 输出 (Windows): "D:\Data\docs\file.txt"
func (a *Adapter) toSysPath(relPath string) string {
	// 这里的 filepath.FromSlash 会自动根据系统处理分隔符
	return filepath.Join(a.rootDir, filepath.FromSlash(relPath))
}

// toRelPath 将本地系统绝对路径转换为统一相对路径
// 输入 (Windows): "D:\Data\docs\file.txt" -> 输出: "docs/file.txt"
func (a *Adapter) toRelPath(fullPath string) (string, error) {
	rel, err := filepath.Rel(a.rootDir, fullPath)
	if err != nil {
		return "", err
	}
	// 统一转为 "/" 分隔符
	return filepath.ToSlash(rel), nil
}

// calculateMD5 计算本地文件的 MD5 值
func (a *Adapter) calculateMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// ListAll 递归扫描本地目录
func (a *Adapter) ListAll() (map[string]*fs.FileMeta, error) {
	files := make(map[string]*fs.FileMeta)
	var errs []error

	err := filepath.Walk(a.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("扫描文件出错 %s: %w", path, err))
			return nil
		}

		// 跳过根目录本身
		if path == a.rootDir {
			return nil
		}

		// 计算相对路径
		relPath, err := a.toRelPath(path)
		if err != nil {
			errs = append(errs, err)
			return nil
		}

		files[relPath] = &fs.FileMeta{
			RelPath: relPath,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			IsDir:   info.IsDir(),
			// Hash is not calculated here for performance reasons
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%d errors occurred during file scan: %v", len(errs), errs)
	}
	return files, nil
}

// OpenStream 打开本地文件读取流
func (a *Adapter) OpenStream(relPath string) (io.ReadCloser, error) {
	fullPath := a.toSysPath(relPath)
	return os.Open(fullPath)
}

// WriteStream 将流写入本地文件
// modTime: 用于恢复文件的修改时间，保持和云端一致
func (a *Adapter) WriteStream(relPath string, stream io.Reader, modTime time.Time) (string, error) {
	fullPath := a.toSysPath(relPath)

	// 1. 确保父目录存在
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 2. 创建文件
	f, err := os.Create(fullPath)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	// 注意：此处不能 defer f.Close()，因为后面还要修改时间，或者需要在 close 后修改

	// 3. 写入数据
	if _, err := io.Copy(f, stream); err != nil {
		f.Close()
		return "", fmt.Errorf("写入数据失败: %w", err)
	}

	// 关闭文件以刷入磁盘
	if err := f.Close(); err != nil {
		return "", err
	}

	// 4. 恢复修改时间 (重要：双向同步依赖这个时间)
	if !modTime.IsZero() {
		if err := os.Chtimes(fullPath, time.Now(), modTime); err != nil {
			slog.Warn("无法修改文件时间", "path", relPath, "err", err)
		}
	}

	// 5. 计算并返回文件的 MD5
	return a.calculateMD5(fullPath)
}

// Delete 删除本地文件
func (a *Adapter) Delete(relPath string) error {
	fullPath := a.toSysPath(relPath)
	return os.RemoveAll(fullPath) // RemoveAll 也可以删除非空目录
}

// Stat 获取单个文件状态
func (a *Adapter) Stat(relPath string) (*fs.FileMeta, error) {
	fullPath := a.toSysPath(relPath)
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}

	var md5Str string
	if !info.IsDir() {
		md5Str, err = a.calculateMD5(fullPath)
		if err != nil {
			// Stat 失败通常应该返回错误
			return nil, fmt.Errorf("stat md5 calc failed: %w", err)
		}
	}

	return &fs.FileMeta{
		RelPath: relPath, // 直接返回传入的
		Size:    info.Size(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
		Hash:    md5Str, // 添加 MD5
	}, nil
}
func (a *Adapter) Rename(oldRelPath, newRelPath string) error {
	oldSysPath := a.toSysPath(oldRelPath)
	newSysPath := a.toSysPath(newRelPath)

	// 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(newSysPath), 0755); err != nil {
		return err
	}

	return os.Rename(oldSysPath, newSysPath)
}
