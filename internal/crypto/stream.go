package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// NewEncryptReader 创建一个加密读取流
// 输入: 明文流 (src)
// 输出: 密文流 (包含头部 IV)
// 原理: [16字节随机IV] + [AES-CTR加密内容]
func NewEncryptReader(src io.Reader, key []byte) (io.Reader, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("无效的密钥: %w", err)
	}

	// 1. 生成随机 IV (16字节)
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("生成 IV 失败: %w", err)
	}

	// 2. 创建 CTR 加密流
	stream := cipher.NewCTR(block, iv)

	// 3. 组合: 先读取 IV，随后读取加密后的数据
	// io.MultiReader 会依次读取各个 Reader
	return io.MultiReader(
		bytes.NewReader(iv),                     // 头部写入 IV
		&cipher.StreamReader{S: stream, R: src}, // 后续写入密文
	), nil
}

// NewDecryptReader 创建一个解密读取流
// 输入: 密文流 (src, 开头必须包含 IV)
// 输出: 明文流
func NewDecryptReader(src io.Reader, key []byte) (io.Reader, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("无效的密钥: %w", err)
	}

	// 1. 读取头部的 IV
	iv := make([]byte, aes.BlockSize)
	// 注意：这里会从 src 中预读 16 字节，剩下的才是密文正文
	if _, err := io.ReadFull(src, iv); err != nil {
		return nil, fmt.Errorf("读取 IV 失败或文件太短: %w", err)
	}

	// 2. 创建 CTR 解密流 (CTR 模式下加密和解密逻辑是一样的)
	stream := cipher.NewCTR(block, iv)

	return &cipher.StreamReader{S: stream, R: src}, nil
}
