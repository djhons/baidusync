package database

import (
	"encoding/json"
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

const (
	// BucketName 是数据库中的“表名”
	BucketName = "FileSnapshots"
)

// DB 封装 BoltDB 实例
type DB struct {
	conn *bbolt.DB
}

// NewBoltDB 初始化并打开数据库
func NewBoltDB(dbPath string) (*DB, error) {
	// 打开数据库，如果文件不存在则创建
	// Timeout 选项防止两个进程同时打开同一个数据库导致死锁
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("打开 BoltDB 失败: %w", err)
	}

	// 确保 Bucket 存在
	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(BucketName))
		return err
	})

	if err != nil {
		db.Close()
		return nil, fmt.Errorf("创建 Bucket 失败: %w", err)
	}

	return &DB{conn: db}, nil
}

// Close 关闭数据库连接
func (d *DB) Close() error {
	return d.conn.Close()
}

// Get 获取单个文件的快照状态
func (d *DB) Get(relPath string) (*FileState, error) {
	var state FileState
	err := d.conn.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketName))
		v := b.Get([]byte(relPath))
		if v == nil {
			return fmt.Errorf("not found") // 简单的哨兵错误
		}
		return json.Unmarshal(v, &state)
	})

	if err != nil {
		if err.Error() == "not found" {
			return nil, nil // 正常情况：没有记录返回 nil
		}
		return nil, err
	}
	return &state, nil
}

// Put 保存或更新文件的快照状态
func (d *DB) Put(state *FileState) error {
	state.LastSyncTime = time.Now().UnixNano()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}

	return d.conn.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketName))
		return b.Put([]byte(state.RelPath), data)
	})
}

// Delete 删除文件的快照记录 (当文件被删除时调用)
func (d *DB) Delete(relPath string) error {
	return d.conn.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketName))
		return b.Delete([]byte(relPath))
	})
}

// ListAll 获取所有缓存的文件状态
// 在同步开始时调用，用于构建 Base 状态树
func (d *DB) ListAll() (map[string]*FileState, error) {
	result := make(map[string]*FileState)

	err := d.conn.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(BucketName))

		return b.ForEach(func(k, v []byte) error {
			var state FileState
			if err := json.Unmarshal(v, &state); err != nil {
				// 如果某条数据损坏，记录日志但不中断整个流程？
				// 这里为了严谨选择返回错误
				return fmt.Errorf("解析数据失败 key=%s: %w", string(k), err)
			}
			result[string(k)] = &state
			return nil
		})
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}
