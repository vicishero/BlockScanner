package processor

import (
	"context"
	"log/slog"

	"blockscanner/entity"
	"blockscanner/store"
)

// AliasHandler 事件处理器接口 — 每种 alias 对应一个实现
type AliasHandler func(ctx context.Context, db *store.DB, event *entity.InfraEvmEventLog) error

// aliasRegistry alias 到处理器的映射表
var aliasRegistry = map[string]AliasHandler{
	// 业务 alias 注册（对应设计文档第 4.4 节已知 alias 列表）
	// 每个 alias 的处理逻辑由业务模块实现并在此注册
	"bind":             defaultHandler("bind"),
	"tokenPurchased":   defaultHandler("tokenPurchased"),
	"marketCreated":    defaultHandler("marketCreated"),
	"bet":              defaultHandler("bet"),
	"listingCreated":   defaultHandler("listingCreated"),
	"listingCancelled": defaultHandler("listingCancelled"),
	"trade":            defaultHandler("trade"),
	"resolved":         defaultHandler("resolved"),
	"claimed":          defaultHandler("claimed"),
	"airdrop":          defaultHandler("airdrop"),
	"purchase":         defaultHandler("purchase"),
	"useCard":          defaultHandler("useCard"),
	"buyAndUse":        defaultHandler("buyAndUse"),
	"nftListed":        defaultHandler("nftListed"),
	"nftSold":          defaultHandler("nftSold"),
	"nftDelisted":      defaultHandler("nftDelisted"),
	"nftPriceUpdated":  defaultHandler("nftPriceUpdated"),
	"nftTransfer":      defaultHandler("nftTransfer"),
}

// KnownAliases 返回已知 alias 列表
func KnownAliases() []string {
	aliases := make([]string, 0, len(aliasRegistry))
	for k := range aliasRegistry {
		aliases = append(aliases, k)
	}
	return aliases
}

// RegisterHandler 注册自定义 alias 处理器（供业务模块调用）
func RegisterHandler(alias string, handler AliasHandler) {
	aliasRegistry[alias] = handler
}

// RouteEvent 根据 alias 路由到对应处理器
// 在 scheduler 的 processScanEvents 中调用
func RouteEvent(ctx context.Context, db *store.DB, event *entity.InfraEvmEventLog) {
	alias := event.Alias

	handler, ok := aliasRegistry[alias]
	if !ok {
		// 未注册的 alias，标记为永久失败
		slog.Warn("unknown alias, marking as failed",
			"alias", alias,
			"event_id", event.ID,
		)
		if err := db.UpdateEventLogStatus(ctx, event.ID, 9); err != nil {
			slog.Error("update event status failed", "event_id", event.ID, "error", err)
		}
		return
	}

	// 执行处理器
	if err := handler(ctx, db, event); err != nil {
		slog.Error("event handler failed",
			"alias", alias,
			"event_id", event.ID,
			"error", err,
		)
		// 可重试失败 → 回滚到 status=0
		if updateErr := db.UpdateEventLogStatus(ctx, event.ID, 0); updateErr != nil {
			slog.Error("rollback event status failed", "event_id", event.ID, "error", updateErr)
		}
		return
	}

	// 成功 → status=2
	if err := db.UpdateEventLogStatus(ctx, event.ID, 2); err != nil {
		slog.Error("mark event completed failed", "event_id", event.ID, "error", err)
		return
	}

	slog.Debug("event processed successfully",
		"alias", alias,
		"event_id", event.ID,
		"tx", event.TxHash,
	)
}

// defaultHandler 默认处理器（仅记录日志，业务逻辑需替换）
func defaultHandler(name string) AliasHandler {
	return func(ctx context.Context, db *store.DB, event *entity.InfraEvmEventLog) error {
		slog.Info("event handled by default handler",
			"alias", name,
			"event_id", event.ID,
			"event_name", event.EventName,
			"decoded_data", event.DecodedData,
		)
		return nil
	}
}
