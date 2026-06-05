package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// JSON-RPC 请求/响应结构
type jsonRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error,omitempty"`
	ID      int             `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RPCClient Ethereum JSON-RPC HTTP 客户端
type RPCClient struct {
	url    string
	client *http.Client
}

// NewRPCClient 创建 JSON-RPC 客户端
func NewRPCClient(rpcURL string) *RPCClient {
	return &RPCClient{
		url: rpcURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// call 执行 JSON-RPC 调用
func (c *RPCClient) call(ctx context.Context, method string, params []interface{}) (json.RawMessage, error) {
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpc call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// 检查 HTTP 状态码
	if resp.StatusCode != 200 {
		preview := string(respBody)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, fmt.Errorf("http status %d: %s", resp.StatusCode, preview)
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		preview := string(respBody)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, fmt.Errorf("unmarshal response: %w (body: %s)", err, preview)
	}

	if len(rpcResp.Error) > 0 && string(rpcResp.Error) != "null" {
		// 尝试解析为标准 JSON-RPC error 对象
		var rpcErr jsonRPCError
		if json.Unmarshal(rpcResp.Error, &rpcErr) == nil && rpcErr.Message != "" {
			return nil, fmt.Errorf("rpc error %d: %s", rpcErr.Code, rpcErr.Message)
		}
		// 可能是字符串形式的错误
		var errStr string
		if json.Unmarshal(rpcResp.Error, &errStr) == nil {
			return nil, fmt.Errorf("rpc error: %s", errStr)
		}
		return nil, fmt.Errorf("rpc error: %s", string(rpcResp.Error))
	}

	return rpcResp.Result, nil
}

// BlockNumber 获取最新块高 (eth_blockNumber)
func (c *RPCClient) BlockNumber(ctx context.Context) (int64, error) {
	result, err := c.call(ctx, "eth_blockNumber", []interface{}{})
	if err != nil {
		return 0, err
	}

	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return 0, fmt.Errorf("unmarshal blockNumber: %w", err)
	}

	return hexToInt64(hex), nil
}

// GetLogs 获取指定范围内的日志 (eth_getLogs)
func (c *RPCClient) GetLogs(ctx context.Context, addresses []string, topics [][]string, fromBlock, toBlock int64) ([]EthLog, error) {
	// 构建 eth_getLogs 参数
	params := map[string]interface{}{
		"fromBlock": int64ToHex(fromBlock),
		"toBlock":   int64ToHex(toBlock),
	}

	// 地址过滤
	if len(addresses) > 0 {
		params["address"] = addresses
	}

	// topic 过滤：二维数组，外层 OR，内层 AND
	if len(topics) > 0 {
		topicFilters := make([][]string, len(topics))
		for i, t := range topics {
			topicFilters[i] = t
		}
		params["topics"] = topicFilters
	}

	result, err := c.call(ctx, "eth_getLogs", []interface{}{params})
	if err != nil {
		return nil, err
	}

	var logs []EthLog
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}

	return logs, nil
}

// EthLog 以太坊日志结构
type EthLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	BlockHash   string   `json:"blockHash"`
	TxHash      string   `json:"transactionHash"`
	LogIndex    string   `json:"logIndex"`
	Removed     bool     `json:"removed"`
}
