# BlockScanner 运维优化设计

日期：2026-06-06

## 背景

本次优化围绕现有 Go 版 BlockScanner 的运行稳定性和运维可观测性展开，需求包括：

1. 去掉启动时自动加入测试数据，后续由用户手动添加链和合约事件配置。
2. 检查并优化管理后台修改配置后的生效机制。
3. RPC 多次失败后发送 Telegram 通知到运营群。
4. 将同步进度和错误信息输出到日志，并补充任务执行日志。

当前项目结构中，启动入口在 `src/main.go`，调度器在 `src/scheduler/scheduler.go`，扫链核心在 `src/scanner/worker.go`，RPC 客户端在 `src/scanner/rpc.go`，数据库访问封装在 `src/store/`。

## 目标

- 启动程序不再自动写入示例链、示例合约事件或其他测试数据。
- 管理后台修改链、合约事件、任务启停和 cron 间隔后，最多 60 秒内被扫描服务感知。
- 单条链 RPC 连续失败 5 次后，向 Telegram 运营群发送告警；恢复后发送一次恢复通知。
- 关键扫描进度、错误、任务执行结果同时写入现有文件日志，并记录到 `infra_job_log`。

## 非目标

- 不新增管理后台接口或 HTTP 服务。
- 不引入消息队列、数据库触发器或近实时变更推送。
- 不把 Telegram 配置放入数据库；本次采用 `config.yaml` 和环境变量。
- 不实现完整业务事件处理逻辑；现有 alias handler 机制保持不变。

## 方案选择

采用“最小改动轮询版”：

- 删除 `main.go` 中 `seedData` 调用及示例数据函数。
- 调度器增加 60 秒刷新循环，周期性从数据库同步链和任务配置。
- 扫链 worker 继续每轮重新读取链配置和合约事件配置。
- 新增 Telegram notifier，配置来自 `config.yaml` 和环境变量。
- Scheduler 按链维护 RPC 连续失败状态，达到阈值后通知。
- 补充结构化日志和 `infra_job_log` 更新。

该方案改动集中，满足 60 秒内生效要求，且不会给当前工程引入额外服务入口。

## 配置设计

在现有 `config.Config` 中新增 Telegram 配置：

```yaml
telegram:
  enabled: false
  bot_token: ""
  chat_id: ""
  rpc_failure_threshold: 5
  cooldown_secs: 1800
```

环境变量覆盖：

- `TELEGRAM_ENABLED`
- `TELEGRAM_BOT_TOKEN`
- `TELEGRAM_CHAT_ID`
- `TELEGRAM_RPC_FAILURE_THRESHOLD`
- `TELEGRAM_COOLDOWN_SECS`

默认值：

- `enabled=false`
- `rpc_failure_threshold=5`
- `cooldown_secs=1800`

如果 Telegram 未启用，或 token/chat id 不完整，notifier 不发送外部请求，只记录 debug/info 日志。Telegram bot token 不输出到日志。

## 自动测试数据移除

当前 `src/main.go` 在数据库初始化后调用 `seedData(db)`，该函数会插入 Polygon、Ethereum 和示例 USDC Transfer 事件。实现时删除该调用和 `seedData` 函数。

启动后的行为变为：

1. 加载配置。
2. 初始化日志。
3. 初始化数据库和迁移。
4. 不插入任何链或合约事件。
5. 启动调度器。
6. 如果数据库无启用链，调度器仅保留事件消费任务或空闲运行，并输出提示日志。

## 配置及时生效设计

### 当前判断

现有调度器只在启动时执行一次 `syncJobs` 和 `ensureJobsForAllChains`。因此：

- 合约事件配置：worker 每轮读取，通常下一轮生效。
- 链扫描参数：worker 每轮读取，batch size、confirmations、start/last block 等下一轮生效。
- RPC URL：当前 cron 执行时会创建 worker，并使用当次读取到的 chain RPC URL，下一次 cron 触发可生效。
- cron 间隔、新增链、禁用链、任务启停：目前不会自动刷新。

### 新行为

调度器启动后：

1. 立即执行一次 `refreshJobs(ctx)`。
2. 启动 `time.Ticker(60 * time.Second)`。
3. 每次 tick 重新同步数据库配置。
4. ctx 取消时停止刷新循环和 cron。

`refreshJobs` 负责：

- 查询启用链。
- 为启用链创建缺失的 `scanEvmChain` job。
- 对已存在的 scan job 只更新 name 和 cron，不强制把人工暂停状态改成启用。
- 确保 `processScanEvent` 任务存在。
- 查询启用 job 并注册/更新 cron。
- 对比内存中的 cron entry 与本轮有效 job key，移除数据库中已禁用、删除、不存在或链已禁用的任务。

链状态优先：如果链被禁用，即使 `infra_job` 中对应 scan job 仍是启用，也不注册该链扫链 cron。这样后台禁用链后最多 60 秒内停止新任务触发。

## RPC 失败 Telegram 通知设计

### 失败范围

只统计明确来自 RPC 的失败：

- `eth_blockNumber` 调用失败。
- `eth_getLogs` 调用失败。
- HTTP 状态非 200。
- JSON-RPC error。
- RPC 请求超时、连接失败、响应读取失败、响应解析失败。

数据库写入失败、事件处理失败、解码失败不计入 RPC 连续失败，但仍记录错误日志和任务日志。

### 错误识别

为避免把所有扫描错误都算作 RPC 错误，RPC 层返回可识别错误类型，例如：

- 新增 `scanner.RPCError` 包装 RPC call 错误；或
- 提供 `scanner.IsRPCError(err)` 判断函数。

`BlockNumber` 和 `GetLogs` 遇到 RPC call 错误时保留该类型信息，上层调度器据此累计失败。

### 状态维护

Scheduler 按 `chain_id` 维护内存状态：

- `consecutiveFailures`
- `lastError`
- `lastNotifiedAt`
- `alerting`

扫描任务执行结果：

- 扫描成功后清零失败计数。
- 如果此前处于 alerting 状态，发送一次“RPC 已恢复”通知。
- RPC 失败时累计失败次数。
- 连续失败次数达到 5 且不在冷却期内，发送 Telegram 告警。
- 告警发送后进入 alerting 状态。
- 冷却期默认 30 分钟，持续故障期间最多按冷却期重复告警。

### 通知内容

告警内容包含：

- 应用名 BlockScanner。
- 链名称和 chain_id。
- 连续失败次数。
- 脱敏后的 RPC URL。
- 错误摘要。
- 当前时间。

恢复内容包含：

- 链名称和 chain_id。
- 脱敏后的 RPC URL。
- 此前连续失败次数。
- 恢复时间。

### URL 脱敏

通知和日志中的 RPC URL 应脱敏：

- 保留 scheme、host、path。
- 隐藏 query string。
- 对 path 中常见 token/key 片段做简化隐藏。

Telegram 发送失败不影响扫块流程，只记录 `component=notifier` 的错误日志。

## 日志设计

### 文件日志

保留现有 `slog` 输出方式：stdout + 按天滚动日志文件。补充结构化字段，便于 grep 和日志系统解析。

统一建议字段：

- `component`: `scheduler` / `scanner` / `rpc` / `notifier` / `processor`
- `chain_id`
- `chain_name`
- `job_id`
- `handler`
- `round`
- `from_block`
- `to_block`
- `confirmed_block`
- `last_synced_block`
- `duration`
- `error`

### 扫链进度

每个扫描批次记录：

- 扫描开始：链、起始块、确认块、上次同步块。
- 批次范围：from、to、batch size、remaining。
- RPC 结果：logs 数量和耗时。
- 写库结果：解码日志数量、实际插入数量、推进后的 last_synced_block。
- 扫描结束：是否还有剩余块、本轮耗时。
- 错误：错误类型、阶段、错误消息。

### 事件消费进度

事件消费任务记录：

- claim 批次数量。
- 成功处理数量。
- 失败/回滚数量。
- 未知 alias 数量。
- 单次任务耗时。

现有 `processor.RouteEvent` 不返回状态，本次可保持最小改动，仅在调度器层记录批次 claim 情况；如果需要精确成功/失败统计，可后续将 `RouteEvent` 改为返回处理结果。

## 任务执行日志表

当前已有 `infra_job_log` 表和 `CreateJobLog` 方法，但 cron 执行未使用。新增 `UpdateJobLog` 方法，用于结束时更新状态、消息和结束时间。

每次 cron 触发：

1. 创建 job log：`status=0`，`start_time=now`。
2. 执行任务。
3. 成功：`status=1`，message 写摘要。
4. 失败：`status=2`，message 写错误。
5. 无法写 job log 时只记录文件日志，不阻断任务执行。

任务日志 message 应简洁，例如：

- `scan completed: chain_id=137 rounds=3 has_more=true duration=2.3s`
- `scan failed: chain_id=137 rpc_error=true error=eth_getLogs: ...`
- `process events completed: batches=2 duration=0.8s`

## 数据流

启动数据流：

1. `main` 加载配置并初始化 logger/db/notifier。
2. `main` 创建 scanner 和 scheduler。
3. `scheduler.Start` 立即刷新 job 并启动 cron。
4. `scheduler.Start` 启动 60 秒刷新循环。

扫描数据流：

1. cron 触发 `scanEvmChain`。
2. scheduler 创建 job log。
3. scheduler 获取链配置。
4. 创建 worker 并最多连续执行 10 轮 `ScanRound`。
5. worker 每轮读取最新链配置和合约事件配置。
6. worker 调 RPC、解码日志、写事件日志、推进 last_synced_block。
7. scheduler 根据结果更新 RPC 失败状态、发送通知、更新 job log。

刷新数据流：

1. ticker 触发 `refreshJobs`。
2. 读取启用链和启用任务。
3. 创建缺失任务，但不覆盖人工暂停。
4. 更新 cron entries。
5. 移除无效 entries。

## 错误处理

- 配置加载失败、数据库初始化失败：保持启动失败并退出。
- Telegram 配置缺失：服务继续运行，仅跳过通知。
- Telegram 发送失败：记录错误，不影响扫块。
- RPC 失败：记录错误，累计失败次数，达到阈值后通知。
- 数据库写入失败：记录错误，任务日志标记失败，不计入 RPC 失败。
- 调度刷新失败：记录错误，下一个 60 秒周期重试。
- cron 表达式非法：记录错误，不注册该任务；下一轮刷新可重试。

## 测试计划

至少覆盖以下场景：

1. 启动不再调用 `seedData`，空数据库不会自动插入示例链和合约事件。
2. `config.Load` 正确读取 Telegram 配置和环境变量覆盖。
3. Telegram disabled 或配置不完整时 notifier 不发送请求。
4. Telegram enabled 时发送告警/恢复消息格式正确。
5. RPC 连续失败 5 次后触发一次告警。
6. RPC 持续失败时受冷却期限制，不刷屏。
7. RPC 从失败恢复时发送一次恢复通知并清零状态。
8. 调度器刷新能添加新增链任务。
9. 调度器刷新能移除禁用链或禁用 job 的 cron entry。
10. 修改 `block_interval_secs` 后 60 秒刷新内更新 cron 表达式。
11. cron 任务执行会创建并更新 `infra_job_log`。
12. 扫描成功、扫描失败、事件消费失败都有文件日志输出。

## 实施边界

本设计聚焦当前四个优化需求。后续如果需要更强后台可观测性，可继续增加：

- 后台配置刷新接口。
- 任务运行状态表。
- 通知渠道抽象，支持 Slack/飞书/企业微信。
- 事件处理结果统计返回值。
