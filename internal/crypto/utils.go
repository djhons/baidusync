package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// ==========================================
// 以下是可选功能：用于加密/解密文件名
// ==========================================

// EncryptName 加密文件名 (AES-GCM + Base64Url)
// 用于将 "report.pdf" 变成 "a8s7df87as..." 这样的乱码存网盘
func EncryptName(plainName string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// GCM 模式适合小数据块（文件名）
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// AES-GCM 推荐的 Nonce 大小是 12 字节
	// 为了实现确定性加密（相同的输入总是产生相同的输出），我们不能使用随机 Nonce。
	// 我们可以从明文本身派生出 Nonce，这在保证“每个文件名对应唯一 Nonce”的前提下是安全的。
	// 使用 SHA-256 哈希来确保良好的分布，并取其前 NonceSize() 个字节。
	nonceHash := sha256.Sum256([]byte(plainName))
	nonce := nonceHash[:aesGCM.NonceSize()]

	// Seal: 加密、附加校验码，并将 Nonce 作为密文的前缀
	// 这样解密时才能正确恢复 Nonce
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plainName), nil)

	// 转为 URL 安全的 Base64，适合作为网盘文件名
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

// DecryptName 解密文件名
func DecryptName(encryptedName string, key []byte) (string, error) {
	data, err := base64.URLEncoding.DecodeString(encryptedName)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("文件名密文太短")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// CalculateHash 计算流的 MD5/SHA1 (用于校验)
// 注意：如果用于大文件，这会消耗 IO。通常直接用 os.File 读取。
// func CalculateHash(...) {}
