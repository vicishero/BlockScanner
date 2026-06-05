package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DailyWriter 按天滚动的日志写入器
// 每天凌晨自动切换到新文件，并清理过期日志
type DailyWriter struct {
	mu         sync.Mutex
	dir        string
	prefix     string
	maxAgeDays int
	curDate    string
	file       *os.File
}

// NewDailyWriter 创建按天滚动的日志写入器
// dir: 日志目录
// prefix: 日志文件名前缀，如 "blockscanner"
// maxAgeDays: 日志保留天数
func NewDailyWriter(dir, prefix string, maxAgeDays int) (*DailyWriter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir %s: %w", dir, err)
	}

	w := &DailyWriter{
		dir:        dir,
		prefix:     prefix,
		maxAgeDays: maxAgeDays,
	}

	if err := w.rotate(); err != nil {
		return nil, err
	}

	// 启动后台 goroutine，每分钟检查是否需要切换日期
	go w.watchDate()

	return w, nil
}

// Write 实现 io.Writer，写入当前日期的日志文件
func (w *DailyWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotate(); err != nil {
		return 0, err
	}

	return w.file.Write(p)
}

// rotate 检查并执行日期切换 + 清理过期文件
func (w *DailyWriter) rotate() error {
	today := time.Now().Format("2006-01-02")
	if today == w.curDate {
		return nil
	}

	// 关闭旧文件
	if w.file != nil {
		w.file.Close()
	}

	// 打开新文件
	filename := filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.prefix, today))
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", filename, err)
	}

	w.file = f
	w.curDate = today

	// 清理过期日志
	go w.cleanOldLogs()

	return nil
}

// watchDate 每 60 秒检查是否需要切换日期（跨天自动切换）
func (w *DailyWriter) watchDate() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		w.mu.Lock()
		_ = w.rotate()
		w.mu.Unlock()
	}
}

// cleanOldLogs 清理超过 maxAgeDays 的日志文件
func (w *DailyWriter) cleanOldLogs() {
	if w.maxAgeDays <= 0 {
		return
	}

	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -w.maxAgeDays)
	prefix := w.prefix + "-"

	// 收集过期文件并按时间排序
	var oldFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			oldFiles = append(oldFiles, name)
		}
	}

	// 保留最近 maxAgeDays 天的文件，删除其余的
	sort.Strings(oldFiles)
	keep := len(oldFiles) - w.maxAgeDays
	if keep < 0 {
		keep = 0
	}

	for i := 0; i < keep; i++ {
		path := filepath.Join(w.dir, oldFiles[i])
		os.Remove(path)
	}
}
