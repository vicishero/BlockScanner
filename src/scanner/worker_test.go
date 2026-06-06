package scanner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"blockscanner/entity"
	"blockscanner/store"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestScanRoundReportsNoRPCTouchedWhenNoContractEvents(t *testing.T) {
	db := newWorkerTestDB(t)
	chain := entity.InfraEvmChain{
		ChainID:           137,
		Name:              "Polygon",
		RPCURL:            "http://127.0.0.1/unused",
		Confirmations:     0,
		BatchSize:         10,
		CatchUpBatchSize:  10,
		BlockIntervalSecs: 2,
		LastSyncedBlock:   0,
		StartBlock:        0,
		Status:            1,
	}
	if err := db.Create(&chain).Error; err != nil {
		t.Fatalf("create chain: %v", err)
	}

	var rpcCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&rpcCalls, 1)
		t.Fatalf("unexpected RPC request for chain with no enabled contract events")
	}))
	defer server.Close()

	worker := NewChainWorker(db, server.URL, chain.ChainID)
	hasMore, touchedRPC, err := worker.ScanRound(context.Background())
	if err != nil {
		t.Fatalf("ScanRound returned error: %v", err)
	}
	if hasMore {
		t.Fatal("hasMore = true, want false")
	}
	if touchedRPC {
		t.Fatal("touchedRPC = true, want false")
	}
	if got := atomic.LoadInt32(&rpcCalls); got != 0 {
		t.Fatalf("RPC calls = %d, want 0", got)
	}
}

func TestScanRoundReportsRPCTouchedAfterBlockNumber(t *testing.T) {
	db := newWorkerTestDB(t)
	chain := entity.InfraEvmChain{
		ChainID:           8453,
		Name:              "Base",
		RPCURL:            "http://127.0.0.1/unused",
		Confirmations:     0,
		BatchSize:         10,
		CatchUpBatchSize:  10,
		BlockIntervalSecs: 2,
		LastSyncedBlock:   10,
		StartBlock:        0,
		Status:            1,
	}
	if err := db.Create(&chain).Error; err != nil {
		t.Fatalf("create chain: %v", err)
	}
	if err := db.Create(&entity.InfraEvmContractEvent{
		ChainID:         chain.ChainID,
		ContractAddress: "0x0000000000000000000000000000000000000001",
		EventSignature:  "Transfer(address,address,uint256)",
		EventName:       "Transfer",
		Topic0:          "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
		Status:          1,
	}).Error; err != nil {
		t.Fatalf("create contract event: %v", err)
	}

	var rpcCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&rpcCalls, 1)
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method != "eth_blockNumber" {
			t.Fatalf("RPC method = %q, want eth_blockNumber", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`"0xa"`), ID: req.ID}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	worker := NewChainWorker(db, server.URL, chain.ChainID)
	hasMore, touchedRPC, err := worker.ScanRound(context.Background())
	if err != nil {
		t.Fatalf("ScanRound returned error: %v", err)
	}
	if hasMore {
		t.Fatal("hasMore = true, want false")
	}
	if !touchedRPC {
		t.Fatal("touchedRPC = false, want true")
	}
	if got := atomic.LoadInt32(&rpcCalls); got != 1 {
		t.Fatalf("RPC calls = %d, want 1", got)
	}
}

func newWorkerTestDB(t *testing.T) *store.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gormDB.AutoMigrate(&entity.InfraEvmChain{}, &entity.InfraEvmContractEvent{}, &entity.InfraEvmEventLog{}, &entity.InfraJob{}, &entity.InfraJobLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return &store.DB{DB: gormDB}
}
