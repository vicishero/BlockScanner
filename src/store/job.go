package store

import (
	"blockscanner/entity"
	"context"
	"time"
)

// GetEnabledJobs 获取所有启用的定时任务
func (d *DB) GetEnabledJobs(ctx context.Context) ([]entity.InfraJob, error) {
	var jobs []entity.InfraJob
	err := d.WithContext(ctx).
		Where("status = 1").
		Where("deleted IS NULL OR deleted = ?", false).
		Find(&jobs).Error
	return jobs, err
}

// UpsertJob 插入或更新定时任务
func (d *DB) UpsertJob(ctx context.Context, job *entity.InfraJob) error {
	// 简单 upsert: 先查后更新或创建
	var existing entity.InfraJob
	err := d.WithContext(ctx).
		Where("handler_name = ? AND handler_param = ?", job.HandlerName, job.HandlerParam).
		Where("deleted IS NULL OR deleted = ?", false).
		First(&existing).Error

	if err == nil {
		// 已存在，更新
		return d.WithContext(ctx).
			Model(&existing).
			Updates(map[string]interface{}{
				"name":            job.Name,
				"cron_expression": job.CronExpression,
			}).Error
	}

	// 不存在，创建
	return d.WithContext(ctx).Create(job).Error
}

// DisableJobByHandlerParam 按 handler_param 禁用任务
func (d *DB) DisableJobByHandlerParam(ctx context.Context, handlerName, handlerParam string) error {
	return d.WithContext(ctx).
		Model(&entity.InfraJob{}).
		Where("handler_name = ? AND handler_param = ?", handlerName, handlerParam).
		Update("status", 2).Error
}

// CreateJobLog 记录任务执行日志
func (d *DB) CreateJobLog(ctx context.Context, jobLog *entity.InfraJobLog) error {
	return d.WithContext(ctx).Create(jobLog).Error
}

// UpdateJobLog 更新任务执行日志
func (d *DB) UpdateJobLog(ctx context.Context, id int64, status int8, message string, endTime time.Time) error {
	return d.WithContext(ctx).
		Model(&entity.InfraJobLog{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":   status,
			"message":  message,
			"end_time": endTime,
		}).Error
}
