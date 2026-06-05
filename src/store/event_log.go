package store

import (
	"blockscanner/entity"
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InsertEventLogBatch 批量插入事件日志（ON CONFLICT DO NOTHING）
// 返回实际插入的行数
func (d *DB) InsertEventLogBatch(ctx context.Context, logs []entity.InfraEvmEventLog) (int64, error) {
	if len(logs) == 0 {
		return 0, nil
	}

	result := d.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&logs)

	return result.RowsAffected, result.Error
}

// ClaimUnprocessedEvents 原子 Claim 未处理的事件
// 将 status=0 的事件批量更新为 status=1（处理中），返回被 Claim 的事件列表
func (d *DB) ClaimUnprocessedEvents(ctx context.Context, limit int) ([]entity.InfraEvmEventLog, error) {
	var events []entity.InfraEvmEventLog

	err := d.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先查询未处理的事件
		if err := tx.
			Where("status = 0").
			Order("id ASC").
			Limit(limit).
			Find(&events).Error; err != nil {
			return err
		}

		if len(events) == 0 {
			return nil
		}

		// 收集 ID
		ids := make([]int64, len(events))
		for i, e := range events {
			ids[i] = e.ID
		}

		// 原子更新为处理中
		return tx.Model(&entity.InfraEvmEventLog{}).
			Where("id IN ? AND status = 0", ids).
			Update("status", 1).Error
	})

	return events, err
}

// UpdateEventLogStatus 更新事件处理状态
func (d *DB) UpdateEventLogStatus(ctx context.Context, id int64, status int8) error {
	return d.WithContext(ctx).
		Model(&entity.InfraEvmEventLog{}).
		Where("id = ?", id).
		Update("status", status).Error
}
