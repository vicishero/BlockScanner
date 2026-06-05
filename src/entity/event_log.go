package entity

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// JSONArray 用于存储 JSON 数组 (topics)
type JSONArray []string

func (j JSONArray) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

func (j *JSONArray) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

// JSONMap 用于存储 JSON 对象 (decoded_data)
type JSONMap map[string]interface{}

func (j JSONMap) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

// InfraEvmEventLog 事件日志原始表 — 扫块与业务处理的核心中间表
type InfraEvmEventLog struct {
	ID              int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ChainID         int64     `gorm:"column:chain_id;not null;uniqueIndex:uk_event_unique;index:idx_block_number,priority:1;index:idx_contract,priority:1" json:"chain_id"`
	ContractAddress string    `gorm:"column:contract_address;type:varchar(42);index:idx_contract,priority:2" json:"contract_address"`
	EventName       string    `gorm:"column:event_name;type:varchar(64);index:idx_contract,priority:3" json:"event_name"`
	Alias           string    `gorm:"column:alias;type:varchar(64)" json:"alias"`
	BlockNumber     int64     `gorm:"column:block_number;index:idx_block_number,priority:2" json:"block_number"`
	BlockHash       string    `gorm:"column:block_hash;type:varchar(66)" json:"block_hash"`
	TxHash          string    `gorm:"column:tx_hash;type:varchar(66);uniqueIndex:uk_event_unique" json:"tx_hash"`
	LogIndex        int       `gorm:"column:log_index;uniqueIndex:uk_event_unique" json:"log_index"`
	Topics          JSONArray `gorm:"column:topics;type:json" json:"topics"`
	RawData         string    `gorm:"column:raw_data;type:text" json:"raw_data"`
	DecodedData     JSONMap   `gorm:"column:decoded_data;type:json" json:"decoded_data"`
	DecodeStatus    int16     `gorm:"column:decode_status;default:0" json:"decode_status"` // 0=失败, 1=成功
	Status          int8      `gorm:"column:status;default:0;index:idx_status" json:"status"` // 0=未处理,1=处理中,2=已完成,9=失败
	CreateTime      time.Time `gorm:"column:create_time;autoCreateTime" json:"create_time"`
}

func (InfraEvmEventLog) TableName() string {
	return "infra_evm_event_log"
}
