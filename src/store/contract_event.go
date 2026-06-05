package store

import (
	"blockscanner/entity"
	"context"
)

// GetEnabledContractEvents 获取指定链上所有启用的合约事件配置
func (d *DB) GetEnabledContractEvents(ctx context.Context, chainID int64) ([]entity.InfraEvmContractEvent, error) {
	var events []entity.InfraEvmContractEvent
	err := d.WithContext(ctx).
		Where("chain_id = ? AND status = 1", chainID).
		Find(&events).Error
	return events, err
}
