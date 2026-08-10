package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"tars/pkg/llm"

	"gopkg.in/yaml.v3"
)

// TraceConfig controls OpenTelemetry span export.
// Spans are only exported to the configured OTLP collectors; there is no
// local file sink. Enabled=false (the default when absent) disables all
// tracing regardless of configured endpoints.
type TraceConfig struct {
	// Enabled is the master switch for tracing. Absent/false = 不产生任何 span。
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// OTLPHTTPEndpoint exports spans to an OTLP/HTTP collector
	// (e.g. "localhost:4318" for Jaeger). Empty disables this exporter.
	OTLPHTTPEndpoint string `yaml:"otlpHttpEndpoint,omitempty" json:"otlpHttpEndpoint,omitempty"`
	// OTLPGrpcEndpoint exports spans to an OTLP/gRPC collector
	// (e.g. "localhost:4317" for Arize Phoenix). Empty disables this exporter.
	OTLPGrpcEndpoint string `yaml:"otlpGrpcEndpoint,omitempty" json:"otlpGrpcEndpoint,omitempty"`
}

// AgentConfig controls the ReAct loop runtime behavior.
type AgentConfig struct {
	// MaxIterations caps the ReAct loop rounds (LLM → tools → LLM ...)
	// before the agent is forced to stop. Prevents runaway token burn on
	// pathological loops; complex multi-file tasks need a generous budget.
	MaxIterations int `yaml:"maxIterations,omitempty" json:"maxIterations,omitempty"`
	// CompressionThreshold is the context-usage ratio (0-1) at which history
	// compression should kick in (compression itself is not implemented yet;
	// the threshold is surfaced in the status bar for now).
	CompressionThreshold float64 `yaml:"compressionThreshold,omitempty" json:"compressionThreshold,omitempty"`
}

// DefaultMaxIterations is used when agent.maxIterations is unset or
// non-positive. 100 covers long multi-file tasks while still bounding cost.
const DefaultMaxIterations = 100

// DefaultCompressionThreshold is used when agent.compressionThreshold is
// unset or out of range.
const DefaultCompressionThreshold = 0.8

// CompressionThresholdOrDefault returns the configured compression threshold,
// falling back to DefaultCompressionThreshold when unset or out of (0,1].
func (c *AppConfig) CompressionThresholdOrDefault() float64 {
	if c == nil || c.Agent == nil || c.Agent.CompressionThreshold <= 0 || c.Agent.CompressionThreshold > 1 {
		return DefaultCompressionThreshold
	}
	return c.Agent.CompressionThreshold
}

type AppConfig struct {
	LLM     *llm.Config  `yaml:"llm,omitempty" json:"llm,omitempty"`
	WorkDir string       `yaml:"workDir,omitempty" json:"workDir,omitempty"`
	Trace   *TraceConfig `yaml:"trace,omitempty" json:"trace,omitempty"`
	Agent   *AgentConfig `yaml:"agent,omitempty" json:"agent,omitempty"`
}

// MaxIterationsOrDefault returns the configured ReAct loop limit, falling
// back to DefaultMaxIterations when unset or non-positive.
func (c *AppConfig) MaxIterationsOrDefault() int {
	if c == nil || c.Agent == nil || c.Agent.MaxIterations <= 0 {
		return DefaultMaxIterations
	}
	return c.Agent.MaxIterations
}

// OTLPEndpoints returns the configured OTLP/HTTP and OTLP/gRPC endpoints
// ("" when unset).
func (c *AppConfig) OTLPEndpoints() (httpEndpoint, grpcEndpoint string) {
	if c == nil || c.Trace == nil {
		return "", ""
	}
	return c.Trace.OTLPHTTPEndpoint, c.Trace.OTLPGrpcEndpoint
}

func LoadAppConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// 展开 ${VAR} / $VAR 形式的环境变量引用，使密钥等敏感值
	// 可以通过环境变量注入而不必写入配置文件
	expanded := os.ExpandEnv(string(data))
	appConfig := &AppConfig{}
	if err = yaml.Unmarshal([]byte(expanded), appConfig); err != nil {
		return nil, err
	}
	return appConfig, nil
}

// SaveAppConfigFile 把 cfg 合并写回 YAML 配置文件。
//
// 与"整体序列化覆盖"不同，这里基于 yaml.Node 做键级合并：
//   - 保留原文件中的注释、键顺序和未知字段；
//   - apiKey 为空字符串时保留文件中的原值（原值可能是 ${ENV_VAR} 引用，
//     避免把展开后的真实密钥写回磁盘，或把引用清空）；
//   - 其他字段空字符串/0 值表示"删除该键"，回退到内置默认值。
func SaveAppConfigFile(path string, cfg *AppConfig) error {
	var root yaml.Node
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(bytes.TrimSpace(data)) > 0:
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse existing config: %w", err)
		}
	case err != nil && !os.IsNotExist(err):
		return err
	}
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
	}
	if len(root.Content) == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	m := root.Content[0]
	if m.Kind != yaml.MappingNode {
		return fmt.Errorf("config root must be a mapping, got node kind %d", m.Kind)
	}

	setOrDelStr(m, "workDir", cfg.WorkDir, false)

	if cfg.LLM != nil {
		// llm 段含列表结构，键级合并成本高且易错，改为整段替换：
		// 段内注释会丢失（段外注释保留）；密钥类字段（apiKey/accessKey/
		// secretKey）为空时按供应商 ID 沿用文件中的原值
		//（保留 ${ENV_VAR} 引用）。
		llmCopy := *cfg.LLM
		oldKeys := readFileProviderKeys(m)
		providers := make([]llm.ProviderConfig, len(llmCopy.Providers))
		copy(providers, llmCopy.Providers)
		for i := range providers {
			saved := oldKeys[providers[i].ID]
			if providers[i].ApiKey == "" {
				providers[i].ApiKey = saved["apiKey"]
			}
			if providers[i].AccessKey == "" {
				providers[i].AccessKey = saved["accessKey"]
			}
			if providers[i].SecretKey == "" {
				providers[i].SecretKey = saved["secretKey"]
			}
		}
		llmCopy.Providers = providers
		node := &yaml.Node{}
		if err := node.Encode(&llmCopy); err != nil {
			return fmt.Errorf("encode llm config: %w", err)
		}
		setNode(m, "llm", node)
	}
	if cfg.Agent != nil {
		am := ensureMap(m, "agent")
		setOrDelInt(am, "maxIterations", int64(cfg.Agent.MaxIterations))
		setOrDelFloat(am, "compressionThreshold", cfg.Agent.CompressionThreshold)
	}
	if cfg.Trace != nil {
		tm := ensureMap(m, "trace")
		if cfg.Trace.Enabled {
			setScalar(tm, "enabled", "!!bool", "true")
		} else {
			delKey(tm, "enabled") // 缺省即停用
		}
		setOrDelStr(tm, "otlpHttpEndpoint", cfg.Trace.OTLPHTTPEndpoint, false)
		setOrDelStr(tm, "otlpGrpcEndpoint", cfg.Trace.OTLPGrpcEndpoint, false)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// readFileProviderKeys 从当前文件的 llm 段读取各供应商的密钥字段原文
// （apiKey/accessKey/secretKey，未展开环境变量），用于写回时保留
// ${ENV_VAR} 引用。返回值：map[供应商ID]map[字段名]值。
func readFileProviderKeys(root *yaml.Node) map[string]map[string]string {
	keys := map[string]map[string]string{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "llm" {
			continue
		}
		lm := root.Content[i+1]
		if lm.Kind != yaml.MappingNode {
			break
		}
		for j := 0; j+1 < len(lm.Content); j += 2 {
			if lm.Content[j].Value != "providers" {
				continue
			}
			seq := lm.Content[j+1]
			if seq.Kind != yaml.SequenceNode {
				continue
			}
			for _, item := range seq.Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				var id string
				fields := map[string]string{}
				for k := 0; k+1 < len(item.Content); k += 2 {
					switch item.Content[k].Value {
					case "id":
						id = item.Content[k+1].Value
					case "apiKey", "accessKey", "secretKey":
						fields[item.Content[k].Value] = item.Content[k+1].Value
					}
				}
				if id != "" {
					keys[id] = fields
				}
			}
		}
		break
	}
	return keys
}

// setNode 设置映射节点中的键对应的任意节点；已存在的键保持原位，
// 并沿用旧值节点的注释。
func setNode(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			value.HeadComment = old.HeadComment
			value.LineComment = old.LineComment
			value.FootComment = old.FootComment
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

// setScalar 设置映射节点中的键值；已存在的键保持原位，并沿用旧值节点的注释。
func setScalar(m *yaml.Node, key, tag, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			m.Content[i+1] = &yaml.Node{
				Kind:        yaml.ScalarNode,
				Tag:         tag,
				Value:       value,
				HeadComment: old.HeadComment,
				LineComment: old.LineComment,
				FootComment: old.FootComment,
			}
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
}

// delKey 从映射节点中删除键（不存在则无事发生）。
func delKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// ensureMap 返回 key 对应的映射节点，不存在则创建。
func ensureMap(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if v := m.Content[i+1]; v.Kind == yaml.MappingNode {
				return v
			}
			nm := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			m.Content[i+1] = nm
			return nm
		}
	}
	nm := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, nm)
	return nm
}

// setOrDelStr：空字符串表示删除（keepWhenEmpty=true 时保留原值，仅用于 apiKey）。
func setOrDelStr(m *yaml.Node, key, value string, keepWhenEmpty bool) {
	if value == "" {
		if !keepWhenEmpty {
			delKey(m, key)
		}
		return
	}
	setScalar(m, key, "!!str", value)
}

func setOrDelInt(m *yaml.Node, key string, value int64) {
	if value == 0 {
		delKey(m, key)
		return
	}
	setScalar(m, key, "!!int", strconv.FormatInt(value, 10))
}

func setOrDelFloat(m *yaml.Node, key string, value float64) {
	if value == 0 {
		delKey(m, key)
		return
	}
	setScalar(m, key, "!!float", strconv.FormatFloat(value, 'f', -1, 64))
}
