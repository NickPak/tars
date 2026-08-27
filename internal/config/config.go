package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"tars/internal/agent"
	"tars/pkg/llm"
	"tars/pkg/skill"
	"tars/pkg/trace"

	"gopkg.in/yaml.v3"
)

var (
	instance      atomic.Pointer[AppConfig]
	AppConfigPath = "config/config.yaml"
)

func DefaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "tars")
	}
	return "."
}

// AppConfig 是用户应用配置（config.yaml）。
// 注意：MCP 服务器配置不属于本结构——它与技能一样由 pkg/mcp.Manager
// 在工作目录（<workDir>/mcp/servers.yaml）下自管读写、即改即存。
type AppConfig struct {
	LLM     *llm.Config   `yaml:"llm,omitempty" json:"llm,omitempty"`
	WorkDir string        `yaml:"workDir,omitempty" json:"workDir,omitempty"`
	Trace   *trace.Config `yaml:"trace,omitempty" json:"trace,omitempty"`
	Agent   *agent.Config `yaml:"agent,omitempty" json:"agent,omitempty"`
	Skills  *skill.Config `yaml:"skills,omitempty" json:"skills,omitempty"`
}

func Get() *AppConfig {
	return instance.Load()
}

func Set(cfg *AppConfig) {
	instance.Store(cfg)
}

func LoadAppConfig() error {
	data, err := os.ReadFile(AppConfigPath)
	if err != nil {
		return err
	}
	expanded := os.ExpandEnv(string(data))
	appConfig := &AppConfig{}
	if err = yaml.Unmarshal([]byte(expanded), appConfig); err != nil {
		return err
	}
	if err := appConfig.Validate(); err != nil {
		slog.Warn("LLM config invalid at startup, continuing", "error", err)
	}
	Set(appConfig)
	return nil
}

func (c *AppConfig) Validate() error {
	if c.Agent == nil {
		c.Agent = &agent.Config{}
	}
	c.Agent.Validate()
	if c.Trace == nil {
		c.Trace = &trace.Config{}
	}
	c.Trace.Validate()
	c.WorkDir = strings.TrimSpace(c.WorkDir)
	if c.WorkDir == "" {
		c.WorkDir = DefaultDataDir()
	}
	if c.Skills == nil {
		c.Skills = &skill.Config{}
	}
	c.Skills.Validate()
	if c.LLM != nil {
		if err := c.LLM.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// SaveAppConfigFile 把 cfg 合并写回 YAML 配置文件。
//
// 与"整体序列化覆盖"不同，这里基于 yaml.Node 做键级合并：
//   - 保留原文件中的注释、键顺序和未知字段；
//   - apiKey 为空字符串时保留文件中的原值（原值可能是 ${ENV_VAR} 引用，
//     避免把展开后的真实密钥写回磁盘，或把引用清空）。
func SaveAppConfigFile(cfg *AppConfig) error {
	var root yaml.Node
	data, err := os.ReadFile(AppConfigPath)
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
		// llm 段为 map 结构，键级合并成本高且易错，改为整段替换：
		// 段内注释会丢失（段外注释保留）；密钥类字段（apiKey/accessKey/
		// secretKey）为空时按供应商 ID（map key）沿用文件中的原值
		//（保留 ${ENV_VAR} 引用）。
		llmCopy := *cfg.LLM
		oldKeys := readFileProviderKeys(m)
		providers := make(map[string]*llm.ProviderConfig, len(llmCopy.Providers))
		for id, p := range llmCopy.Providers {
			cp := *p
			saved := oldKeys[id]
			if cp.ApiKey == "" {
				cp.ApiKey = saved["apiKey"]
			}
			if cp.AccessKey == "" {
				cp.AccessKey = saved["accessKey"]
			}
			if cp.SecretKey == "" {
				cp.SecretKey = saved["secretKey"]
			}
			providers[id] = &cp
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
		setScalar(am, "maxIterations", "!!int", strconv.FormatInt(int64(cfg.Agent.MaxIterations), 10))
		setScalar(am, "compressionThreshold", "!!float", strconv.FormatFloat(cfg.Agent.CompressionThreshold, 'f', -1, 64))
		setScalar(am, "compressionKeepTurns", "!!int", strconv.FormatInt(int64(cfg.Agent.CompressionKeepTurns), 10))
		setScalar(am, "compressionMinBatch", "!!int", strconv.FormatInt(int64(cfg.Agent.CompressionMinBatch), 10))
		setScalar(am, "iterationTimeout", "!!str", cfg.Agent.IterationTimeout.String())
	}
	if cfg.Skills != nil {
		sm := ensureMap(m, "skills")
		setOrDelInt(sm, "tierFullMax", int64(cfg.Skills.TierFullMax))
		setOrDelInt(sm, "tierResidentMax", int64(cfg.Skills.TierResidentMax))
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
	return os.WriteFile(AppConfigPath, buf.Bytes(), 0644)
}

// readFileProviderKeys 从当前文件的 llm 段读取各供应商的密钥字段原文
// （apiKey/accessKey/secretKey，未展开环境变量），用于写回时保留
// ${ENV_VAR} 引用。返回值：map[供应商ID]map[字段名]值。
// 供应商在文件中以 map 表达，key 即供应商 ID。
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
			pm := lm.Content[j+1]
			if pm.Kind != yaml.MappingNode {
				continue
			}
			// map 形式：key 即供应商 ID，value 是供应商配置映射
			for k := 0; k+1 < len(pm.Content); k += 2 {
				id := pm.Content[k].Value
				item := pm.Content[k+1]
				if item.Kind != yaml.MappingNode {
					continue
				}
				fields := map[string]string{}
				for n := 0; n+1 < len(item.Content); n += 2 {
					switch item.Content[n].Value {
					case "apiKey", "accessKey", "secretKey":
						fields[item.Content[n].Value] = item.Content[n+1].Value
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
