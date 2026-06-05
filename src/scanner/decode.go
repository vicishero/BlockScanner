package scanner

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// eventRegex 解析 Solidity 事件签名: Name(type1 indexed name1, type2 name2, ...)
// 例如: Transfer(address indexed from, address indexed to, uint256 value)
var eventRegex = regexp.MustCompile(`^(\w+)\((.+)\)$`)

// paramRegex 解析单个参数: type indexed? name?
var paramRegex = regexp.MustCompile(`^\s*([\w\[\]]+)(?:\s+indexed)?(?:\s+(\w+))?\s*$`)

// ParsedEvent 解析后的事件信息
type ParsedEvent struct {
	Name     string
	Topic0   string // keccak256 哈希
	ABI      *abi.Event
	ABIInst  abi.ABI
}

// ParseEventSignature 解析事件签名，返回事件名称、topic0 和 ABI
// 输入: "Transfer(address indexed from, address indexed to, uint256 value)"
// 输出: ParsedEvent{Name: "Transfer", Topic0: "0x...", ABI: ...}
func ParseEventSignature(signature string) (*ParsedEvent, error) {
	matches := eventRegex.FindStringSubmatch(strings.TrimSpace(signature))
	if len(matches) != 3 {
		return nil, fmt.Errorf("invalid event signature format: %s", signature)
	}

	eventName := matches[1]
	paramsStr := matches[2]

	// 构建 ABI JSON
	abiInputs, err := parseParamsToABIInputs(paramsStr)
	if err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	// 构建规范签名（不含参数名，不含 indexed）
	canonicalTypes := make([]string, len(abiInputs))
	for i, inp := range abiInputs {
		canonicalTypes[i] = inp.Type
	}
	canonicalSig := fmt.Sprintf("%s(%s)", eventName, strings.Join(canonicalTypes, ","))

	// 计算 topic0 = keccak256(canonicalSig)
	topic0 := "0x" + common.Bytes2Hex(crypto.Keccak256([]byte(canonicalSig)))

	// 构建 ABI JSON 结构
	abiJSON := map[string]interface{}{
		"type":      "event",
		"name":      eventName,
		"anonymous": false,
		"inputs":    abiInputs,
	}

	abiBytes, err := json.Marshal([]map[string]interface{}{abiJSON})
	if err != nil {
		return nil, fmt.Errorf("marshal abi json: %w", err)
	}

	parsedABI, err := abi.JSON(strings.NewReader(string(abiBytes)))
	if err != nil {
		return nil, fmt.Errorf("parse abi: %w", err)
	}

	event, ok := parsedABI.Events[eventName]
	if !ok {
		return nil, fmt.Errorf("event %s not found in parsed ABI", eventName)
	}

	return &ParsedEvent{
		Name:     eventName,
		Topic0:   topic0,
		ABI:      &event,
		ABIInst:  parsedABI,
	}, nil
}

// abiInput 用于构建 ABI JSON
type abiInput struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Indexed bool   `json:"indexed"`
}

// parseParamsToABIInputs 解析参数字符串为 ABI Inputs
func parseParamsToABIInputs(paramsStr string) ([]abiInput, error) {
	if strings.TrimSpace(paramsStr) == "" {
		return []abiInput{}, nil
	}

	// 按顶层逗号分割（需处理嵌套括号）
	parts := splitParams(paramsStr)
	inputs := make([]abiInput, 0, len(parts))

	for i, part := range parts {
		matches := paramRegex.FindStringSubmatch(part)
		if len(matches) < 2 {
			return nil, fmt.Errorf("invalid param format: %s", part)
		}

		solType := normalizeType(matches[1])
		indexed := strings.Contains(part, "indexed")
		name := matches[2]
		if name == "" {
			name = fmt.Sprintf("param%d", i)
		}

		inputs = append(inputs, abiInput{
			Name:    name,
			Type:    solType,
			Indexed: indexed,
		})
	}

	return inputs, nil
}

// splitParams 按顶层逗号分割参数字符串（处理嵌套的括号）
func splitParams(s string) []string {
	var parts []string
	depth := 0
	current := strings.Builder{}

	for _, c := range s {
		switch c {
		case '(':
			depth++
			current.WriteRune(c)
		case ')':
			depth--
			current.WriteRune(c)
		case ',':
			if depth == 0 {
				parts = append(parts, current.String())
				current.Reset()
			} else {
				current.WriteRune(c)
			}
		default:
			current.WriteRune(c)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// normalizeType 规范化 Solidity 类型名
func normalizeType(t string) string {
	t = strings.TrimSpace(t)
	// uint 和 int 别名处理: uint -> uint256, int -> int256
	if t == "uint" {
		return "uint256"
	}
	if t == "int" {
		return "int256"
	}
	return t
}

// DecodeLog 解码单条以太坊日志
// 返回 JSON map，索引参数从 topics[1:] 获取，非索引参数从 data 获取
func DecodeLog(parsed *ParsedEvent, ethLog EthLog) (map[string]interface{}, error) {
	// 准备 topics（去掉 topic0 后的 indexed 参数值）
	nonIndexed, indexed := splitInputs(parsed.ABI.Inputs)

	topics := ethLog.Topics
	if len(topics) == 0 {
		return nil, fmt.Errorf("empty topics")
	}

	// indexed 参数从 topics[1:] 中获取
	indexedValues := make(map[string]interface{})
	for i, inp := range indexed {
		topicIdx := i + 1 // topics[0] 是 topic0（事件签名哈希）
		if topicIdx < len(topics) {
			val, err := decodeIndexedParam(topics[topicIdx], inp.Type.String())
			if err != nil {
				slog.Warn("decode indexed param failed",
					"name", inp.Name,
					"type", inp.Type,
					"error", err,
				)
				val = topics[topicIdx] // fallback: 原始 hex
			}
			indexedValues[inp.Name] = val
		}
	}

	// non-indexed 参数从 data 解码
	dataValues := make(map[string]interface{})
	if len(nonIndexed) > 0 && ethLog.Data != "" && ethLog.Data != "0x" {
		dataBytes := common.FromHex(ethLog.Data)
		vals, err := decodeNonIndexedParams(dataBytes, nonIndexed)
		if err != nil {
			slog.Warn("decode non-indexed params failed",
				"error", err,
			)
			// fallback: 返回原始 data
			dataValues["_raw"] = ethLog.Data
		} else {
			for i, inp := range nonIndexed {
				if i < len(vals) {
					dataValues[inp.Name] = toJSONValue(vals[i])
				}
			}
		}
	}

	// 合并结果
	result := make(map[string]interface{})
	for k, v := range indexedValues {
		result[k] = v
	}
	for k, v := range dataValues {
		result[k] = v
	}

	return result, nil
}

// splitInputs 将 ABI inputs 分为 indexed 和 non-indexed 两组
func splitInputs(inputs abi.Arguments) (nonIndexed, indexed abi.Arguments) {
	for _, inp := range inputs {
		if inp.Indexed {
			indexed = append(indexed, inp)
		} else {
			nonIndexed = append(nonIndexed, inp)
		}
	}
	return
}

// decodeIndexedParam 解码索引参数（从 topic 中）
// indexed 参数存储在 topic 中，是 32 字节的 hex 值
func decodeIndexedParam(topicHex string, solType string) (interface{}, error) {
	topicBytes := common.FromHex(topicHex)

	switch normalizeType(solType) {
	case "address":
		// address 是 20 字节左填充到 32 字节
		if len(topicBytes) >= 12 {
			addr := common.BytesToAddress(topicBytes[12:])
			return addr.Hex(), nil
		}
		return topicHex, nil

	case "bool":
		if len(topicBytes) > 0 {
			for i := 0; i < len(topicBytes)-1; i++ {
				if topicBytes[i] != 0 {
					return topicHex, nil
				}
			}
			return topicBytes[len(topicBytes)-1] != 0, nil
		}
		return false, nil

	default:
		// 数值类型和 bytes32，返回 hex 字符串
		return topicHex, nil
	}
}

// decodeNonIndexedParams 从 data 中解码非索引参数
func decodeNonIndexedParams(data []byte, inputs abi.Arguments) ([]interface{}, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	// 收集类型用于解码
	types := make([]string, len(inputs))
	for i, inp := range inputs {
		types[i] = inp.Type.String()
	}

	// 使用 go-ethereum 的 ABI 解码
	typeList, err := buildTypeList(types)
	if err != nil {
		return nil, err
	}

	// 使用 abi 包的 unpack 逻辑
	return unpack(data, typeList)
}

// buildTypeList 将 Solidity 类型字符串转为 abi.Type 列表
func buildTypeList(types []string) ([]abi.Type, error) {
	// 构建临时的函数 ABI 来解析类型
	funcABI := `[{"name":"temp","type":"function","inputs":[`
	inputParts := make([]string, len(types))
	for i, t := range types {
		inputParts[i] = fmt.Sprintf(`{"name":"p%d","type":"%s"}`, i, t)
	}
	funcABI += strings.Join(inputParts, ",")
	funcABI += `],"outputs":[]}]`

	parsed, err := abi.JSON(strings.NewReader(funcABI))
	if err != nil {
		return nil, fmt.Errorf("parse types: %w", err)
	}

	method, ok := parsed.Methods["temp"]
	if !ok {
		return nil, fmt.Errorf("temp method not found")
	}

	result := make([]abi.Type, len(method.Inputs))
	for i, inp := range method.Inputs {
		result[i] = inp.Type
	}
	return result, nil
}

// unpack 解码 ABI 编码的数据
func unpack(data []byte, types []abi.Type) ([]interface{}, error) {
	if len(types) == 0 {
		return nil, nil
	}

	// 构建一个包含所有类型的 tuple
	// 使用 abi.Arguments 的 Unpack 方法
	args := make(abi.Arguments, len(types))
	for i, t := range types {
		args[i] = abi.Argument{Type: t}
	}

	return args.Unpack(data)
}

// toJSONValue 将 Solidity 解码值转为 JSON 友好的值
func toJSONValue(v interface{}) interface{} {
	switch val := v.(type) {
	case [32]byte:
		return common.Bytes2Hex(val[:])
	case []byte:
		return common.Bytes2Hex(val)
	case common.Address:
		return val.Hex()
	default:
		return v
	}
}
