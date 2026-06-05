package entity

import "time"

// InfraEvmChain EVM链配置表
type InfraEvmChain struct {
	ID                   int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ChainID              int64     `gorm:"column:chain_id;uniqueIndex:uk_chain_id;not null" json:"chain_id"`
	Name                 string    `gorm:"column:name;type:varchar(32);not null" json:"name"`
	RPCURL               string    `gorm:"column:rpc_url;type:varchar(256);not null" json:"rpc_url"`
	BlockIntervalSecs    int       `gorm:"column:block_interval_secs;default:12" json:"block_interval_secs"`
	Confirmations        int       `gorm:"column:confirmations;default:6" json:"confirmations"`
	BatchSize            int       `gorm:"column:batch_size;default:2000" json:"batch_size"`
	CatchUpBatchSize     int       `gorm:"column:catch_up_batch_size;default:5000" json:"catch_up_batch_size"`
	CatchUpIntervalSecs  int       `gorm:"column:catch_up_interval_secs;default:1" json:"catch_up_interval_secs"`
	StartBlock           int64     `gorm:"column:start_block;default:0" json:"start_block"`
	LastSyncedBlock      int64     `gorm:"column:last_synced_block;default:0" json:"last_synced_block"`
	Status               int8      `gorm:"column:status;default:1" json:"status"` // 0=禁用, 1=启用
	CreateTime           time.Time `gorm:"column:create_time;autoCreateTime" json:"create_time"`
	UpdateTime           time.Time `gorm:"column:update_time;autoUpdateTime" json:"update_time"`
}

func (InfraEvmChain) TableName() string {
	return "infra_evm_chain"
}
