# MCP 服务器本地候选清单

按 **3.3 MCP 安全审查清单**逐项审查通过的可用服务器。每接入一个外部
MCP 服务器，必须先填本表的审查记录（"审查 MCP 时"检查动作见
`plan/agent-tool-design-plan.md` 附录）。

## 审查记录模板

| 服务器 | 版本（锁定） | 能力边界 | description 审计 | 风险提示 | 审查日期 | 审查人 |
|---|---|---|---|---|---|---|
| （示例）yahoo-finance | 0.3.2 | 只读：股票行情/财经数据 | 无注入指令/无凭证索取/无 URI 外联 | 无（只读查询，risk: low） | 2026-08-19 | — |

审查要点（逐项打钩）：
1. **版本锁定**：配置中 command/args 钉住具体版本（如 `yahoo-finance-mcp@0.3.2`），不用 `latest`；
2. **能力边界**：列出该服务器能做什么/不能做什么（只读 vs 可写、数据范围）；
3. **description 审计**：工具与服务器描述是**不可信输入**——检查是否含"调用前先执行 X""请提供 API key"之类的注入指令；
4. **风险提示**：危险操作是否清晰标注、是否需用户确认；risk 级别如何设定及理由；
5. **致命三要素检查**：是否同时满足"不可信内容访问 + 私密数据访问 + 外部通信"——若是，暂缓接入。

## 手工冒烟验证（#11 验收）

用真实 stdio 服务器验证全链路（设置页 → 系统消息 → discover → 审批 → 调用）：

1. **配置**：设置 → MCP 与工具 → 添加服务器
   - 名字：`yahoo-finance`，命令：`npx`，参数：`-y yahoo-finance-mcp@0.3.2`
   - 描述：`stock quotes and financial data`，类型 `query`，风险 `low`（只读）
   - 添加 → 保存 → 打开启用开关 → 保存；
2. **探测**：点"探测"——按钮转圈后提示"工具清单已缓存"，条目显示 `N 个工具`；
3. **系统消息**：新建会话，发任意消息——系统消息应含 `# Available MCP Servers` 与 `yahoo-finance [query]` 行；
4. **发现**：提问"查一下 AAPL 的股价"——Agent 应调用 `discover_tools`，结果中出现 `mcp__yahoo-finance__get_stock_price (now registered...)`；
5. **审批与调用**：risk=low 时直接调用；把服务器 risk 改 `medium` 保存后重试——应弹出审批（摘要含参数）；
6. **状态栏**：轮次中 `<tools registered="mcp__yahoo-finance__get_stock_price"/>` 出现；
7. **延迟启动确认**：会话启动时任务管理器无 `yahoo-finance` 进程；discover 命中后进程出现；禁用并保存后进程退出。

## 备注

- 嵌入索引 + 两级路由为**高级选型**（plan 3.4）：候选条目超过数百、BM25
  排序质量不足时才启用；当前（技能 + MCP 工具合计百量级）不需要。
