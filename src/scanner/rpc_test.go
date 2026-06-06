package scanner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsRPCErrorDetectsWrappedHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewRPCClient(server.URL)
	_, err := client.BlockNumber(context.Background())
	if err == nil {
		t.Fatalf("BlockNumber returned nil error")
	}
	if !IsRPCError(err) {
		t.Fatalf("IsRPCError(%v) = false, want true", err)
	}

	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error does not unwrap to RPCError: %v", err)
	}
	if rpcErr.Method != "eth_blockNumber" {
		t.Fatalf("RPCError.Method = %q, want eth_blockNumber", rpcErr.Method)
	}
}

func TestIsRPCErrorReturnsFalseForNonRPCError(t *testing.T) {
	if IsRPCError(errors.New("database failed")) {
		t.Fatalf("IsRPCError returned true for non-RPC error")
	}
}
