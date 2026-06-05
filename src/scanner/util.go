package scanner

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// hexToInt64 将 0x 开头的 hex 字符串转为 int64
func hexToInt64(hex string) int64 {
	hex = strings.TrimPrefix(hex, "0x")
	if hex == "" {
		return 0
	}
	n := new(big.Int)
	n.SetString(hex, 16)
	return n.Int64()
}

// int64ToHex 将 int64 转为 0x 开头的 hex 字符串
func int64ToHex(n int64) string {
	return fmt.Sprintf("0x%x", n)
}

// hexToInt 将 0x 开头的 hex 字符串转为 int
func hexToInt(hex string) int {
	return int(hexToInt64(hex))
}

// validateEVMAddress 校验 EVM 地址格式
func validateEVMAddress(addr string) error {
	if len(addr) != 42 {
		return fmt.Errorf("address length must be 42, got %d: %s", len(addr), addr)
	}
	if !strings.HasPrefix(addr, "0x") {
		return fmt.Errorf("address must start with 0x: %s", addr)
	}
	for _, c := range addr[2:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("address contains invalid hex character: %s", addr)
		}
	}
	return nil
}

// validateRPCURL 安全校验 RPC URL（防 SSRF）
func validateRPCURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("rpc_url is empty")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("rpc_url must start with http:// or https://")
	}

	// 禁止内网地址
	forbiddenHosts := []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"0.0.0.0",
		"10.",
		"172.16.", "172.17.", "172.18.", "172.19.",
		"172.20.", "172.21.", "172.22.", "172.23.",
		"172.24.", "172.25.", "172.26.", "172.27.",
		"172.28.", "172.29.", "172.30.", "172.31.",
		"192.168.",
	}

	lower := strings.ToLower(rawURL)
	for _, forbidden := range forbiddenHosts {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("rpc_url contains forbidden host pattern: %s", forbidden)
		}
	}

	return nil
}

// parseHexToBig 将 hex 字符串解析为 big.Int
func parseHexToBig(hex string) (*big.Int, error) {
	hex = strings.TrimPrefix(hex, "0x")
	if hex == "" {
		return big.NewInt(0), nil
	}
	n := new(big.Int)
	_, ok := n.SetString(hex, 16)
	if !ok {
		return nil, fmt.Errorf("invalid hex: %s", hex)
	}
	return n, nil
}

// parseHexToInt 将 hex 字符串转为 int
func parseHexToInt(hex string) (int, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(hex, "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse hex int %s: %w", hex, err)
	}
	return int(n), nil
}
