package baidu

import (
	"fmt"
	"io"
	"log/slog" // Add slog import
	"path"     // 仅用于处理 URL 风格路径
	"strings"
	"time"

	"baidusync/internal/crypto" // Add crypto import
	"baidusync/internal/fs"
)

// Adapter 实现了 fs.FileSystem 接口
type Adapter struct {
	client *Client
	root   string // 网盘根目录，例如 "/apps/cloudsync"

	// 新增字段用于文件名加密
	encryptKey       []byte
	encryptFilenames bool
}

// NewAdapter 创建适配器实例
func NewAdapter(client *Client, rootDir string, encryptKey []byte, encryptFilenames bool) *Adapter {
	// 确保 root 路径格式正确 (以 / 开头，不以 / 结尾)
	cleanRoot := path.Clean(rootDir)
	if !strings.HasPrefix(cleanRoot, "/") {
		cleanRoot = "/" + cleanRoot
	}
	return &Adapter{
		client:           client,
		root:             cleanRoot,
		encryptKey:       encryptKey,
		encryptFilenames: encryptFilenames,
	}
}

// Root 返回根目录
func (a *Adapter) Root() string {
	return a.root
}

// toAbsPath 将相对路径转换为网盘绝对路径
// relPath: "docs/file.txt" -> abs: "/apps/cloudsync/docs/file.txt"
func (a *Adapter) toAbsPath(relPath string) string {
	return path.Join(a.root, relPath)
}

// =========================================================
// [修改点]: 手动实现 Rel 逻辑，替代 path.Rel
// =========================================================
// toRelPath 将网盘绝对路径转换为相对路径
// absPath: "/apps/cloudsync/docs/file.txt" -> rel: "docs/file.txt"
func (a *Adapter) toRelPath(absPath string) (string, error) {
	// 1. 确保路径格式一致
	cleanAbs := path.Clean(absPath)

	// 2. 检查 absPath 是否真的在 root 目录下
	if !strings.HasPrefix(cleanAbs, a.root) {
		return "", fmt.Errorf("路径错误: 文件 %s 不在根目录 %s 下", cleanAbs, a.root)
	}

	// 3. 去掉前缀
	rel := strings.TrimPrefix(cleanAbs, a.root)

	// 4. 去掉开头的 "/" (TrimPrefix 可能会留下 "/docs/...")
	rel = strings.TrimPrefix(rel, "/")

	// 5. 如果为空，说明就是根目录本身
	if rel == "" {
		return ".", nil // 或者返回 "" 取决于你的业务逻辑，通常 "." 表示当前目录
	}

	return rel, nil
}

// encryptPath 加密路径的每个部分
func (a *Adapter) encryptPath(plainRelPath string) (string, error) {
	if plainRelPath == "" || plainRelPath == "." {
		return "", nil
	}
	parts := strings.Split(plainRelPath, "/")
	encryptedParts := make([]string, len(parts))
	for i, part := range parts {
		encrypted, err := crypto.EncryptName(part, a.encryptKey)
		if err != nil {
			return "", fmt.Errorf("加密路径 '%s' 的部分 '%s' 失败: %w", plainRelPath, part, err)
		}
		encryptedParts[i] = encrypted
	}
	return path.Join(encryptedParts...), nil
}

// decryptPath 解密路径的每个部分
func (a *Adapter) decryptPath(encryptedRelPath string) (string, error) {
	if encryptedRelPath == "" || encryptedRelPath == "." {
		return "", nil
	}
	parts := strings.Split(encryptedRelPath, "/")
	decryptedParts := make([]string, len(parts))
	for i, part := range parts {
		decrypted, err := crypto.DecryptName(part, a.encryptKey)
		if err != nil {
			// 在 ListAll 期间，可能会遇到非加密目录（如旧的），需要优雅处理
			slog.Debug("解密路径失败 (可能为非加密文件)，当成普通文件名处理", "path", encryptedRelPath, "part", part)
			decryptedParts[i] = part // 解密失败，当它是明文
		} else {
			decryptedParts[i] = decrypted
		}
	}
	return path.Join(decryptedParts...), nil
}

// toEncryptedAbsPath 将明文相对路径转换为加密后的网盘绝对路径
func (a *Adapter) toEncryptedAbsPath(plainRelPath string) (string, error) {
	if !a.encryptFilenames {
		return a.toAbsPath(plainRelPath), nil
	}
	encryptedRel, err := a.encryptPath(plainRelPath)
	if err != nil {
		return "", err
	}
	return path.Join(a.root, encryptedRel), nil
}

// toDecryptedRelPath 将加密的网盘绝对路径转换为明文相对路径
func (a *Adapter) toDecryptedRelPath(encryptedAbsPath string) (string, error) {
	encryptedRel, err := a.toRelPath(encryptedAbsPath)
	if err != nil {
		return "", err
	}
	if !a.encryptFilenames {
		return encryptedRel, nil
	}
	return a.decryptPath(encryptedRel)
}

// ListAll 递归列出所有文件
func (a *Adapter) ListAll() (map[string]*fs.FileMeta, error) {
	result := make(map[string]*fs.FileMeta)
	var errs []error
	// 队列中始终使用明文的相对路径
	queue := []string{""} // 从根目录（相对路径为空）开始

	for len(queue) > 0 {
		currentPlainRel := queue[0]
		queue = queue[1:]

		// 将明文相对路径转换为加密后的绝对路径用于 API 调用
		absEncryptedPath, err := a.toEncryptedAbsPath(currentPlainRel)
		if err != nil {
			errs = append(errs, fmt.Errorf("无法创建加密路径，跳过 %s: %w", currentPlainRel, err))
			continue
		}

		// 使用加密路径列出目录内容
		files, err := a.client.ListDir(absEncryptedPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("列出云端目录失败，跳过 %s: %w", absEncryptedPath, err))
			continue
		}

		for _, f := range files {
			// f.ServerName 是加密后的文件名，需要解密
			var plainName string
			if a.encryptFilenames {
				decrypted, derr := crypto.DecryptName(f.ServerName, a.encryptKey)
				if derr != nil {
					errs = append(errs, fmt.Errorf("解密文件名失败 %s: %w", f.ServerName, derr))
					plainName = f.ServerName // 解密失败，直接使用
				} else {
					plainName = decrypted
				}
			} else {
				plainName = f.ServerName
			}

			// 拼接明文的相对路径
			plainRelPath := path.Join(currentPlainRel, plainName)

			if f.IsDir == 1 {
				queue = append(queue, plainRelPath)
			} else {
				result[plainRelPath] = &fs.FileMeta{
					RelPath:    plainRelPath,
					Size:       f.Size,
					ModTime:    time.Unix(f.ServerMTime, 0),
					IsDir:      false,
					RemoteHash: f.MD5,
				}
			}
		}
	}

	if len(errs) > 0 {
		return result, fmt.Errorf("%d errors occurred during remote scan: %v", len(errs), errs)
	}
	return result, nil
}

// OpenStream 打开下载流
func (a *Adapter) OpenStream(relPath string) (io.ReadCloser, error) {
	absPath, err := a.toEncryptedAbsPath(relPath)
	if err != nil {
		return nil, err
	}
	return a.client.Download(absPath)
}

// WriteStream 上传流
func (a *Adapter) WriteStream(relPath string, stream io.Reader, perm time.Time) (string, error) {
	absPath, err := a.toEncryptedAbsPath(relPath)
	if err != nil {
		return "", err
	}
	// 网盘不需要设置上传时间，自动为当前时间
	return a.client.Upload(absPath, stream, 0)
}

// Delete 删除文件
func (a *Adapter) Delete(relPath string) error {
	absPath, err := a.toEncryptedAbsPath(relPath)
	if err != nil {
		return err
	}
	return a.client.Delete(absPath)
}

// Stat 获取单个文件元数据
func (a *Adapter) Stat(relPath string) (*fs.FileMeta, error) {
	// Stat 比较特殊，我们需要获取父目录的内容，然后查找解密后的名字
	dirPlain := path.Dir(relPath)
	namePlain := path.Base(relPath)

	dirEncrypted, err := a.toEncryptedAbsPath(dirPlain)
	if err != nil {
		return nil, err
	}

	list, err := a.client.ListDir(dirEncrypted)
	if err != nil {
		return nil, err
	}

	for _, f := range list {
		var plainName string
		if a.encryptFilenames {
			decrypted, derr := crypto.DecryptName(f.ServerName, a.encryptKey)
			if derr != nil {
				plainName = f.ServerName
			} else {
				plainName = decrypted
			}
		} else {
			plainName = f.ServerName
		}

		if plainName == namePlain {
			return &fs.FileMeta{
				RelPath:    relPath,
				Size:       f.Size,
				ModTime:    time.Unix(f.ServerMTime, 0),
				IsDir:      f.IsDir == 1,
				RemoteHash: f.MD5,
			}, nil
		}
	}

	return nil, fmt.Errorf("file not found: %s", relPath)
}

// Rename 重命名文件
func (a *Adapter) Rename(oldRelPath, newRelPath string) error {
	absOldPath, err := a.toEncryptedAbsPath(oldRelPath)
	if err != nil {
		return err
	}

	oldDir := path.Dir(oldRelPath)
	newDir := path.Dir(newRelPath)

	if oldDir != newDir {
		return fmt.Errorf("暂不支持跨目录重命名 (oldDir: %s, newDir: %s)", oldDir, newDir)
	}

	// newName 需要是加密后的
	var newNameEncrypted string
	if a.encryptFilenames {
		baseName := path.Base(newRelPath)
		encrypted, err := crypto.EncryptName(baseName, a.encryptKey)
		if err != nil {
			return fmt.Errorf("加密新文件名失败: %w", err)
		}
		newNameEncrypted = encrypted
	} else {
		newNameEncrypted = path.Base(newRelPath)
	}

	return a.client.Rename(absOldPath, newNameEncrypted)
}
