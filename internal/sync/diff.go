package sync

import (
	"baidusync/internal/database"
	"baidusync/internal/fs"
	"log/slog"
	"time"
)

// 如果你的 crypto 包没有导出 HeaderSize，也可以在这里定义
// 假设前面的 crypto 实现是 [MD5(32) + IV(16)]，这里就是 48
// 如果回退到最初只加 IV 的方案，这里就是 16
// 根据你的描述 "网盘大小 = 本地大小 + 加密头部开销 (假设是 16 字节 IV)"
const EncryptedOverhead = 16

// compare 决策函数
func (e *Engine) compare(relPath string, local *fs.FileMeta, remote *fs.FileMeta, base *database.FileState) OpType {
	// 1. 处理目录
	if (local != nil && local.IsDir) || (remote != nil && remote.IsDir) {
		return OpIgnore
	}

	// 2. 数据库中没有记录 (Base == nil) -> 灾难恢复/首次初始化
	if base == nil {
		if local != nil && remote == nil {
			return OpUpload
		}
		if local == nil && remote != nil {
			return OpDownload
		}
		if local != nil && remote != nil {
			// 【关键逻辑】DB丢失后的关联策略: 模糊匹配
			if e.isSameFileFuzzy(local, remote) {
				slog.Info("模糊匹配成功，准备重建索引", "path", relPath)
				// 返回 OpIgnore，Engine 层会检测到 base==nil 从而触发 rebuildIndex
				return OpIgnore
			}
			slog.Warn("模糊匹配失败，视为冲突", "path", relPath,
				"localSize", local.Size, "remoteSize", remote.Size)
			return OpConflict
		}
		return OpIgnore
	}

	// 3. 本地文件已消失
	if local == nil {
		if remote == nil {
			return OpIgnore
		}
		if isRemoteSameAsBase(remote, base, len(e.opts.EncryptKey) > 0) {
			return OpDeleteRemote
		}
		return OpDownload
	}

	// 4. 云端文件已消失
	if remote == nil {
		if isLocalSameAsBase(local, base) {
			return OpDeleteLocal
		}
		return OpUpload
	}

	// 5. 双向存在，检查具体变更
	localChanged := !isLocalSameAsBase(local, base)
	remoteChanged := !isRemoteSameAsBase(remote, base, len(e.opts.EncryptKey) > 0)

	if !localChanged && !remoteChanged {
		return OpIgnore
	}
	if localChanged && !remoteChanged {
		return OpUpload
	}
	if !localChanged && remoteChanged {
		return OpDownload
	}

	return OpConflict
}

// isSameFileFuzzy 模糊匹配：本地明文 vs 云端密文
func (e *Engine) isSameFileFuzzy(l, r *fs.FileMeta) bool {
	// 当数据库丢失时，我们只依赖大小进行模糊匹配。
	// 这是一个弱关联，但足以在首次运行时重建索引。
	// 后续的同步将依赖于数据库中的强校验 (Hash)。
	// ModTime 在云端存储中是不可靠的，因此在这里不予比较。

	// 1. 校验大小关系：云端大小 == 本地大小 + 加密头部
	// 如果未开启加密（key为空），则大小应该相等
	expectedRemoteSize := l.Size
	if len(e.opts.EncryptKey) > 0 {
		expectedRemoteSize += EncryptedOverhead
	}

	return r.Size == expectedRemoteSize
}

// isLocalSameAsBase (保持不变或微调)
func isLocalSameAsBase(l *fs.FileMeta, b *database.FileState) bool {
	// 如果有 Hash 记录且 adapter 支持计算，优先比对 Hash
	if l.Hash != "" && b.LocalHash != "" {
		return l.Hash == b.LocalHash
	}
	if l.Size != b.FileSize {
		return false
	}

	baseTime := time.Unix(0, b.ModTime)
	diff := l.ModTime.Sub(baseTime)
	if diff < 0 {
		diff = -diff
	}
	return diff < 2*time.Second
}

func isRemoteSameAsBase(r *fs.FileMeta, b *database.FileState, encrypted bool) bool {
	// 如果有 RemoteHash 记录，优先比对
	if r.RemoteHash != "" && b.RemoteHash != "" {
		return r.RemoteHash == b.RemoteHash
	}
	// 比对大小 (注意：b.FileSize 存的是本地明文大小)
	expectedSize := b.FileSize
	// 只有当引擎配置了加密密钥时，才考虑加密开销
	if encrypted {
		expectedSize += EncryptedOverhead
	}

	return r.Size == expectedSize
}
