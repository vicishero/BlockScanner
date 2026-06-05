package entity

import "time"

// InfraEvmContractEvent 合约事件配置表
type InfraEvmContractEvent struct {
	ID              int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ChainID         int64     `gorm:"column:chain_id;not null;uniqueIndex:uk_chain_contract_topic0" json:"chain_id"`
	ContractAddress string    `gorm:"column:contract_address;type:varchar(42);not null;uniqueIndex:uk_chain_contract_topic0" json:"contract_address"`
	EventSignature  string    `gorm:"column:event_signature;type:varchar(256);not null" json:"event_signature"`
	EventName       string    `gorm:"column:event_name;type:varchar(64)" json:"event_name"`
	Alias           string    `gorm:"column:alias;type:varchar(64);default:''" json:"alias"`
	Topic0          string    `gorm:"column:topic0;type:varchar(66);uniqueIndex:uk_chain_contract_topic0" json:"topic0"`
	StartBlock      int64     `gorm:"column:start_block;default:0" json:"start_block"`
	LastSyncedBlock int64     `gorm:"column:last_synced_block;default:0" json:"last_synced_block"`
	Status          int8      `gorm:"column:status;default:1" json:"status"` // 0=禁用, 1=启用
	CreateTime      time.Time `gorm:"column:create_time;autoCreateTime" json:"create_time"`
	UpdateTime      time.Time `gorm:"column:update_time;autoUpdateTime" json:"update_time"`
}

func (InfraEvmContractEvent) TableName() string {
	return "infra_evm_contract_event"
}
