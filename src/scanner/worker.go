package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"blockscanner/entity"
	"blockscanner/store"

	"gorm.io/gorm"
)

// ChainWorker 单链扫描核心执行单元
// 每次执行只扫一个批次
type ChainWorker struct {
	db       *store.DB
	client   *RPCClient
	chainID  int64
	decoders map[string]*ParsedEvent // key: topic0, 预编译的事件解码器
	mu       sync.RWMutex
}

// NewChainWorker 创建链扫描 worker
func NewChainWorker(db *store.DB, rpcURL string, chainID int64) *ChainWorker {
	return &ChainWorker{
		db:       db,
		client:   NewRPCClient(rpcURL),
		chainID:  chainID,
		decoders: make(map[string]*ParsedEvent),
	}
}

// ScanRound 执行一次扫描轮次
// 返回:
//
//	hasMore: true = 还有剩余块需要继续扫
//	err:     错误信息
func (w *ChainWorker) ScanRound(ctx context.Context) (hasMore bool, err error) {
	// 1. 重新加载链配置（获取最新 last_synced_block）
	chain, err := w.db.GetChainByID(ctx, w.chainID)
	if err != nil {
		return false, fmt.Errorf("get chain %d: %w", w.chainID, err)
	}

	// 2. 查询该链所有启用的合约事件配置
	contractEvents, err := w.db.GetEnabledContractEvents(ctx, w.chainID)
	if err != nil {
		return false, fmt.Errorf("get contract events: %w", err)
	}
	if len(contractEvents) == 0 {
		slog.Debug("no enabled contract events, skip", "chain_id", w.chainID)
		return false, nil
	}

	// 3. 调用 eth_blockNumber 获取最新块高
	latest, err := w.client.BlockNumber(ctx)
	if err != nil {
		return false, fmt.Errorf("eth_blockNumber: %w", err)
	}

	// 4. 计算确认后的安全块高
	confirmed := latest - int64(chain.Confirmations)
	if confirmed < 0 {
		confirmed = 0
	}

	// 5. 计算起始块高
	fromBlock := max(chain.LastSyncedBlock, chain.StartBlock)
	if fromBlock > 0 {
		fromBlock++ // 从已同步的下一块开始
	}
	if fromBlock == 0 && chain.StartBlock > 0 {
		fromBlock = chain.StartBlock
	} else if fromBlock == 0 {
		fromBlock = 1 // 从第 1 块开始
	}

	// 6. 若已追到最新块，返回 false
	if fromBlock > confirmed {
		slog.Debug("already synced to latest confirmed block",
			"chain_id", w.chainID,
			"from", fromBlock,
			"confirmed", confirmed,
		)
		return false, nil
	}

	// 7. 收集所有 address 和 topic0（去重）
	addrSet := make(map[string]struct{})
	topic0Set := make(map[string]struct{})
	eventAliasMap := make(map[string]string) // topic0 -> alias

	for _, ev := range contractEvents {
		addrSet[ev.ContractAddress] = struct{}{}
		topic0Set[ev.Topic0] = struct{}{}
		if ev.Alias != "" {
			eventAliasMap[ev.Topic0] = ev.Alias
		}
		// 预加载/更新解码器
		w.loadDecoder(&ev)
	}

	addresses := make([]string, 0, len(addrSet))
	for addr := range addrSet {
		addresses = append(addresses, addr)
	}
	topic0s := make([]string, 0, len(topic0Set))
	for t0 := range topic0Set {
		topic0s = append(topic0s, t0)
	}

	// 8. 判断追块模式
	remaining := confirmed - fromBlock + 1
	var batchSize int64
	if remaining > int64(chain.BatchSize) {
		// 追块模式：落后超过 batch_size
		batchSize = int64(chain.CatchUpBatchSize)
		slog.Debug("catch-up mode",
			"chain_id", w.chainID,
			"remaining", remaining,
			"batch_size", batchSize,
		)
	} else {
		batchSize = int64(chain.BatchSize)
	}

	// 9. 计算本次扫描的结束块高
	toBlock := fromBlock + batchSize - 1
	if toBlock > confirmed {
		toBlock = confirmed
	}

	slog.Info("scanning blocks",
		"chain_id", w.chainID,
		"from", fromBlock,
		"to", toBlock,
		"batch_size", batchSize,
		"remaining", remaining,
	)

	// 10. 调用 eth_getLogs
	// topics 过滤: [[topic0_a, topic0_b]] — 所有 topic0 放在第一层，表示 OR
	topicFilter := [][]string{topic0s}
	logs, err := w.client.GetLogs(ctx, addresses, topicFilter, fromBlock, toBlock)
	if err != nil {
		return false, fmt.Errorf("eth_getLogs: %w", err)
	}

	if len(logs) == 0 {
		// 没有日志，直接更新块高（空块也要推进）
		slog.Debug("no logs in range, advancing block",
			"chain_id", w.chainID,
			"from", fromBlock,
			"to", toBlock,
		)
		err = w.db.UpdateLastSyncedBlock(ctx, chain.ID, toBlock)
		if err != nil {
			return false, fmt.Errorf("update last_synced_block: %w", err)
		}
		return toBlock < confirmed, nil
	}

	slog.Info("fetched logs",
		"chain_id", w.chainID,
		"count", len(logs),
		"block_range", fmt.Sprintf("%d-%d", fromBlock, toBlock),
	)

	// 11. 按 (address, topic0) 分组并解码
	eventLogs := make([]entity.InfraEvmEventLog, 0, len(logs))
	for _, ethLog := range logs {
		if ethLog.Removed {
			continue // 跳过因重组被移除的日志
		}
		if len(ethLog.Topics) == 0 {
			continue
		}

		topic0 := ethLog.Topics[0]
		alias := eventAliasMap[topic0]
		eventName := ""

		// 解码
		decoder := w.getDecoder(topic0)
		var decodedData map[string]interface{}
		var decodeStatus int16

		if decoder != nil {
			eventName = decoder.Name
			decoded, err := DecodeLog(decoder, ethLog)
			if err != nil {
				slog.Warn("decode log failed",
					"tx", ethLog.TxHash,
					"topic0", topic0,
					"error", err,
				)
				decodedData = map[string]interface{}{"_raw": ethLog.Data}
				decodeStatus = 0
			} else {
				decodedData = decoded
				decodeStatus = 1
			}
		} else {
			decodedData = map[string]interface{}{"_raw": ethLog.Data}
			decodeStatus = 0
		}

		// 处理 topics 数组
		topics := make([]string, len(ethLog.Topics))
		for i, t := range ethLog.Topics {
			topics[i] = t
		}

		eventLog := entity.InfraEvmEventLog{
			ChainID:         w.chainID,
			ContractAddress: ethLog.Address,
			EventName:       eventName,
			Alias:           alias,
			BlockNumber:     hexToInt64(ethLog.BlockNumber),
			BlockHash:       ethLog.BlockHash,
			TxHash:          ethLog.TxHash,
			LogIndex:        hexToInt(ethLog.LogIndex),
			Topics:          topics,
			RawData:         ethLog.Data,
			DecodedData:     decodedData,
			DecodeStatus:    decodeStatus,
			Status:          0, // 未处理
		}
		eventLogs = append(eventLogs, eventLog)
	}

	// 12. 开启事务：批量插入日志 + 更新进度
	err = w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// a. 逐条插入日志（ON CONFLICT DO NOTHING）
		inserted, insertErr := w.db.InsertEventLogBatch(ctx, eventLogs)
		if insertErr != nil {
			return fmt.Errorf("insert logs: %w", insertErr)
		}

		slog.Debug("inserted logs",
			"chain_id", w.chainID,
			"total", len(eventLogs),
			"inserted", inserted,
		)

		// b. 更新链的 last_synced_block
		updateErr := w.db.UpdateLastSyncedBlock(ctx, chain.ID, toBlock)
		if updateErr != nil {
			return fmt.Errorf("update last_synced_block: %w", updateErr)
		}

		return nil
	})

	if err != nil {
		slog.Error("scan round transaction failed, will retry",
			"chain_id", w.chainID,
			"error", err,
		)
		// 等待 5 秒后由上层重试
		time.Sleep(5 * time.Second)
		return true, err
	}

	// 13. 判断是否还有剩余块
	hasMore = toBlock < confirmed
	return hasMore, nil
}

// loadDecoder 加载或更新事件解码器到缓存
func (w *ChainWorker) loadDecoder(ev *entity.InfraEvmContractEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, ok := w.decoders[ev.Topic0]; ok {
		return // 已缓存
	}

	parsed, err := ParseEventSignature(ev.EventSignature)
	if err != nil {
		slog.Error("parse event signature failed",
			"signature", ev.EventSignature,
			"error", err,
		)
		return
	}

	w.decoders[ev.Topic0] = parsed
	slog.Debug("loaded event decoder",
		"event_name", parsed.Name,
		"topic0", parsed.Topic0,
	)
}

// getDecoder 从缓存获取解码器
func (w *ChainWorker) getDecoder(topic0 string) *ParsedEvent {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.decoders[topic0]
}
