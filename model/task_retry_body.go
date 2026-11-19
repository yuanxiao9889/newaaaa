package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Small TEXT rows avoid database packet/column limits and keep large request
// bytes out of the task row rewritten by every poll. Immutable chunks are
// available to every node sharing the primary database.
type TaskRetryBodyChunk struct {
	SnapshotID string `gorm:"primaryKey;size:64"`
	PartIndex  int    `gorm:"primaryKey;autoIncrement:false"`
	Payload    string `gorm:"type:text;not null"`
	ExpiresAt  int64  `gorm:"index"`
}

type TaskRetryBodyRef struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

const taskRetryBodyChunkBytes = 32 << 10

// SaveTaskRetryBody runs before the initial upstream POST. A failed transaction
// cannot leave a partial usable snapshot. The TTL collects abandoned requests;
// normal terminal/window-end cleanup is part of the task CAS transaction.
func SaveTaskRetryBody(ctx context.Context, storage common.BodyStorage) (*TaskRetryBodyRef, error) {
	reader, err := storage.NewReader()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	ref := &TaskRetryBodyRef{ID: common.GetUUID(), Size: storage.Size()}
	hash := sha256.New()
	var size int64
	// SQL error logs must not echo private request bytes from INSERT values.
	err = DB.WithContext(ctx).Session(&gorm.Session{Logger: gormlogger.Discard}).Transaction(func(tx *gorm.DB) error {
		buffer := make([]byte, taskRetryBodyChunkBytes)
		chunks := make([]TaskRetryBodyChunk, 0, 32)
		expires := time.Now().Add(24 * time.Hour).Unix()
		for part := 0; ; part++ {
			n, readErr := io.ReadFull(reader, buffer)
			if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
				return readErr
			}
			if n > 0 {
				size += int64(n)
				if size > ref.Size {
					return fmt.Errorf("retry request snapshot size changed")
				}
				_, _ = hash.Write(buffer[:n])
				chunks = append(chunks, TaskRetryBodyChunk{SnapshotID: ref.ID, PartIndex: part, Payload: base64.StdEncoding.EncodeToString(buffer[:n]), ExpiresAt: expires})
			}
			// Bounded batches keep each statement below the database packet
			// limit without a database round trip for every 32 KiB fragment.
			if len(chunks) > 0 && (len(chunks) == cap(chunks) || readErr != nil) {
				if err := tx.Create(&chunks).Error; err != nil {
					return err
				}
				chunks = chunks[:0]
			}
			if readErr != nil {
				break
			}
		}
		if size != ref.Size || size == 0 {
			return fmt.Errorf("retry request snapshot size mismatch")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ref.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	return ref, nil
}

func (ref *TaskRetryBodyRef) Read(ctx context.Context) ([]byte, error) {
	if ref == nil || ref.Size <= 0 || len(ref.ID) != 32 || len(ref.SHA256) != 64 {
		return nil, fmt.Errorf("invalid retry request snapshot")
	}
	var data bytes.Buffer
	part := 0
	for int64(data.Len()) < ref.Size {
		var chunks []TaskRetryBodyChunk
		if err := DB.WithContext(ctx).Where("snapshot_id = ? AND part_index >= ?", ref.ID, part).Order("part_index").Limit(32).Find(&chunks).Error; err != nil {
			return nil, err
		}
		if len(chunks) == 0 {
			return nil, fmt.Errorf("retry request snapshot is missing")
		}
		for _, chunk := range chunks {
			if chunk.PartIndex != part {
				return nil, fmt.Errorf("retry request snapshot is incomplete")
			}
			decoded, err := base64.StdEncoding.DecodeString(chunk.Payload)
			if err != nil || len(decoded) == 0 || len(decoded) > taskRetryBodyChunkBytes || int64(data.Len()+len(decoded)) > ref.Size {
				return nil, fmt.Errorf("invalid retry request snapshot chunk")
			}
			_, _ = data.Write(decoded)
			part++
		}
	}
	if fmt.Sprintf("%x", sha256.Sum256(data.Bytes())) != ref.SHA256 {
		return nil, fmt.Errorf("retry request snapshot checksum mismatch")
	}
	return data.Bytes(), nil
}

func DeleteTaskRetryBody(ctx context.Context, ref *TaskRetryBodyRef) error {
	if ref == nil {
		return nil
	}
	return DB.WithContext(ctx).Where("snapshot_id = ?", ref.ID).Delete(&TaskRetryBodyChunk{}).Error
}

func PruneTaskRetryBodies(ctx context.Context, now int64) error {
	// One snapshot at a time bounds cleanup work and avoids dialect-specific
	// DELETE LIMIT syntax. A snapshot expires long after its maximum window.
	var chunk TaskRetryBodyChunk
	if err := DB.WithContext(ctx).Where("expires_at < ?", now).Limit(1).Find(&chunk).Error; err != nil {
		return err
	}
	if chunk.SnapshotID == "" {
		return nil
	}
	return DB.WithContext(ctx).Where("snapshot_id = ? AND expires_at < ?", chunk.SnapshotID, now).Delete(&TaskRetryBodyChunk{}).Error
}
