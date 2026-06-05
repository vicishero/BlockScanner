package entity

import (
	"database/sql"
	"time"
)

// InfraJob 定时任务表
type InfraJob struct {
	ID             int64        `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name           string       `gorm:"column:name;type:varchar(64);not null" json:"name"`
	HandlerName    string       `gorm:"column:handler_name;type:varchar(64);not null" json:"handler_name"`
	HandlerParam   string       `gorm:"column:handler_param;type:varchar(256)" json:"handler_param"`
	CronExpression string       `gorm:"column:cron_expression;type:varchar(32);not null" json:"cron_expression"`
	Status         int8         `gorm:"column:status;default:1" json:"status"` // 0=禁用, 1=启用, 2=暂停
	Deleted        sql.NullBool `gorm:"column:deleted;type:bit(1)" json:"deleted"`
	CreateTime     time.Time    `gorm:"column:create_time;autoCreateTime" json:"create_time"`
	UpdateTime     time.Time    `gorm:"column:update_time;autoUpdateTime" json:"update_time"`
}

func (InfraJob) TableName() string {
	return "infra_job"
}

// InfraJobLog 任务执行日志表
type InfraJobLog struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	JobID      int64     `gorm:"column:job_id;not null;index" json:"job_id"`
	Status     int8      `gorm:"column:status;default:0" json:"status"` // 0=执行中, 1=成功, 2=失败
	Message    string    `gorm:"column:message;type:text" json:"message"`
	StartTime  time.Time `gorm:"column:start_time" json:"start_time"`
	EndTime    time.Time `gorm:"column:end_time" json:"end_time"`
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime" json:"create_time"`
}

func (InfraJobLog) TableName() string {
	return "infra_job_log"
}
