package store

import (
	"fmt"
	"log/slog"
	"time"

	"blockscanner/config"
	"blockscanner/entity"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB 数据库连接封装
type DB struct {
	*gorm.DB
}

// NewDB 创建数据库连接并执行自动迁移
func NewDB(cfg config.DBConfig) (*DB, error) {
	gormLogger := logger.New(
		slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	gormDB, err := gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
		Logger:                                   gormLogger,
		SkipDefaultTransaction:                   true,
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	// 自动迁移表结构
	if err := autoMigrate(gormDB); err != nil {
		return nil, fmt.Errorf("failed to auto migrate: %w", err)
	}

	slog.Info("database connected and migrated successfully")

	return &DB{DB: gormDB}, nil
}

// autoMigrate 自动建表
func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&entity.InfraEvmChain{},
		&entity.InfraEvmContractEvent{},
		&entity.InfraEvmEventLog{},
		&entity.InfraJob{},
		&entity.InfraJobLog{},
	)
}

// Close 关闭数据库连接
func (d *DB) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
