package store

import (
	"blockscanner/entity"
	"context"
)

// GetEnabledChains 获取所有启用的链配置
func (d *DB) GetEnabledChains(ctx context.Context) ([]entity.InfraEvmChain, error) {
	var chains []entity.InfraEvmChain
	err := d.WithContext(ctx).
		Where("status = 1").
		Find(&chains).Error
	return chains, err
}

// GetChainByID 根据 chain_id 获取单条链配置
func (d *DB) GetChainByID(ctx context.Context, chainID int64) (*entity.InfraEvmChain, error) {
	var chain entity.InfraEvmChain
	err := d.WithContext(ctx).
		Where("chain_id = ? AND status = 1", chainID).
		First(&chain).Error
	if err != nil {
		return nil, err
	}
	return &chain, nil
}

// UpdateLastSyncedBlock 更新链的 last_synced_block
func (d *DB) UpdateLastSyncedBlock(ctx context.Context, id int64, blockNum int64) error {
	return d.WithContext(ctx).
		Model(&entity.InfraEvmChain{}).
		Where("id = ?", id).
		Update("last_synced_block", blockNum).Error
}

// GetChain 根据主键 ID 获取链配置
func (d *DB) GetChain(ctx context.Context, id int64) (*entity.InfraEvmChain, error) {
	var chain entity.InfraEvmChain
	err := d.WithContext(ctx).Where("id = ?", id).First(&chain).Error
	if err != nil {
		return nil, err
	}
	return &chain, nil
}
