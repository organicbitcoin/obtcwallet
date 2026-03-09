# 面向 AI Agent 与命令行用户的 OBTC 钱包接口设计（草案）

## 1. 为什么 OBTC 应该优先面向 AI agent

OBTC 和传统 BTC 钱包有一个根本区别：资产不是“放着就行”，而是天然带有 **expiry -> expiring -> renew / REAP** 的生命周期。

这意味着：

- 人类钱包更像“余额查看器 + 转账工具”。
- OBTC 钱包更像“资产生命周期控制器 + 风险调度器”。

人类擅长临时决策，但不擅长持续 7 年跟踪、批量预算控制、窗口期执行、脚本轮换和多账户策略一致性。AI agent 则天然适合做这些持续性、规则化、事件驱动的动作。

因此，OBTC 的长期默认钱包形态不应该只是 GUI 或手工 RPC，而应该是：

**一个面向 agent 的、带策略和授权边界的钱包操作系统。**

但这里的“面向 agent”不等于“排斥人类”。

更准确的目标应该是：

- 同一套钱包内核，同时服务 AI 和人类
- 人类优先通过 CLI 使用
- 程序优先通过 gRPC / HTTP / PSBT / SDK 使用
- GUI 可以以后再做，但不应反向决定钱包核心接口

---

## 1.1 双用户定位：一个内核，两类入口

这套 `obtcwallet` 不应该分裂成：

- 一套给 AI 的钱包
- 一套给人类的命令行钱包

正确方向应该是：

- **一个共享内核**
  - UTXO/账户/地址/expiry/renew/policy/signer/audit 都只实现一遍
- **两类交互面**
  - 人类：CLI
  - 程序：gRPC / HTTP / PSBT / SDK

这有三个直接好处：

- 避免 AI 路径和人类路径规则漂移
- 避免出现“两套续期逻辑、两套审计口径”
- CLI 天然可以成为程序接口的薄封装，降低维护成本

本文档不讨论 GUI，但明确要求：**必须提供程序接口，CLI 也应尽量基于同一套程序接口构建。**

---

## 2. 当前 `obtcwallet` 已有的基础能力

从现有代码看，`obtcwallet` 已经具备搭建 AI 钱包底座的几个关键部件：

- 钱包创建与加载：
  - `rpc/api.proto`
  - `rpc/rpcserver/server.go`
  - 已有 `CreateWallet` / `OpenWallet` / `StartConsensusRpc`
- 命令行入口：
  - `cmd/renewall/main.go`
  - 当前已经有面向人类操作员的 CLI 雏形
- 密钥分层加密与内存清零：
  - `waddrmgr/manager.go`
  - `snacl/snacl.go`
  - `internal/zero/slice.go`
- OBTC 到期感知与续期：
  - `wallet/expiry.go`
  - `rpc/legacyrpc/obtc_methods.go`
  - `wallet/autorenew.go`
- PSBT 资金编排与签名：
  - `wallet/psbt.go`

这些能力说明当前仓库已经不是“只能手工转账”的钱包，而是具备演进为 agent wallet 的技术基础。

---

## 2.1 从代码能直接确认的现实约束

为了避免把设计做成“脱离仓库现实的空想稿”，这里把几件当前已经成立的事实单独写清楚：

- 现有 gRPC loader 的 `CreateWallet` 请求直接接收：
  - `public_passphrase`
  - `private_passphrase`
  - `seed`
- 当前 RPC server 在处理完创建请求后，会显式清零请求中的 `private_passphrase` 和 `seed`
- 当前 `waddrmgr` 会把根 HD 私钥以加密形式保存到数据库中，解锁时再把账户私钥和相关密钥材料解密到内存
- 当前 `waddrmgr`/`snacl`/`internal/zero` 已具备“加密存储 + lock 时清零”的基础机制
- 当前钱包已经有 PSBT 资金编排和最终签名能力，可直接作为 AI 接口的执行底座
- 当前 OBTC 到期查询和 auto-renew 仍有本地硬编码参数，不能直接作为长期可靠的链感知接口

这些现实约束意味着：

- 现有代码适合作为 **agent wallet 内核**
- 但还不适合作为 **agent wallet 产品接口**

---

## 3. 当前模型对 AI 来说还不够的地方

### 3.1 仍然是“单钱包 + 全局解锁”模型

当前模型的核心路径是：

- 用 `CreateWallet` 直接传入 seed 和 passphrase。
- 用 `walletpassphrase` 或内部 `Unlock` 做全局解锁。
- 在解锁窗口内完成一组签名操作。

这更适合人工操作，不适合多 agent 协作。AI 需要的是：

- 细粒度权限，而不是“谁拿到口令谁就能做所有事”
- 可撤销 capability，而不是全局 unlock
- 面向操作的短时授权，而不是长时间解锁

### 3.2 还缺少“观察 / 规划 / 签名 / 审计”分层

对 agent 来说，钱包至少应该拆成四层：

- 观察层：看状态，不碰私钥
- 规划层：生成 plan / PSBT / renewal proposal
- 签名层：只负责批准后的签名
- 审计层：记录“谁在何时基于什么策略做了什么”

当前实现里这些能力存在，但边界还没有作为产品接口显式建立出来。

### 3.3 还缺少链参数动态发现

当前这个缺口已经开始收敛，但还没有完全闭环。

现在钱包侧已经新增了一个共享 expiry policy resolver，会按下面的优先级取值：

1. 真实 `obtcd/chaincfg/params_obtc.go`
2. 内置的 OBTC 网络 fallback
3. compatibility default

当前 resolver 支持两个运行时入口：

- `OBTC_CHAINCFG_PATH`
  - 可指向 `params_obtc.go` 文件本身
  - 或 `chaincfg/` 目录
  - 或 `obtcd` repo 根目录
- `OBTC_EXPIRY_NETWORK`
  - 当钱包当前链参数还不能直接表达 `obtcmainnet/obtctestnet/obtcregtest` 时，可显式指定逻辑网络名

共享 resolver 现在已经接到了：

- `rpc/legacyrpc/obtc_methods.go`
- `wallet/autorenew.go`
- `rpc/rpcserver/agentwallet_server.go`

而 `obtcd` 侧的 source-of-truth 仍然是：

- `obtcd/chaincfg/params_obtc.go`

所以当前状态是：

- 已经不再只依赖单点硬编码
- 但仍然没有做到“直接链接 obtcd module 后自动获得 OBTC chaincfg API”

对 AI 钱包来说，最终目标仍然应该是后者，因为 agent 不能长期依赖本地 fallback 去判断 expiry。

### 3.4 还缺少机器友好的操作语义

AI 不适合直接消费“立即执行”的原始命令式接口，尤其不适合：

- 直接 broadcast
- 无 `dry_run`
- 无 operation id
- 无幂等键
- 无保留 / 租约机制
- 无失败原因结构化输出

AI 更适合：

- `preview -> approve -> sign -> publish`
- `observe -> decide -> reserve -> execute -> audit`

---

## 4. 面向 AI 的设计原则

建议把未来接口统一按以下原则设计：

### 4.1 Capability First，不再以全局口令为中心

核心授权单位不应是“钱包口令”，而应是：

- `capability`
- `session`
- `policy`

例如一个 `renewal-agent` 应该只被允许：

- 查看 expiring UTXO
- 生成 renewal 计划
- 在预算和费率阈值内请求签名

它不应自动获得：

- 任意转账权限
- 任意导出密钥权限
- 任意变更钱包配置权限

### 4.2 Intent First，不让 agent 直接拼底层交易

推荐让 AI 先表达“意图”，再由钱包系统生成交易方案。

典型 intent：

- `renew_utxos`
- `transfer_budget`
- `sweep_change`
- `consolidate_dust`
- `fund_psbt`

这样可以把链规则、手续费估算、找零策略、脚本轮换、expiry 风险判断统一放进钱包层。

### 4.3 Watch-Only Planning，Isolated Signing

默认推荐把钱包拆成两个服务：

- `observer/planner`
  - 同步链
  - 管理 UTXO、expiry、策略、事件流
  - 只持有 xpub / watch-only 数据
- `signer`
  - 持有 seed / root key / private key
  - 不负责同步链，不做复杂业务编排
  - 只接受经过 capability 和 policy 检查的签名请求

### 4.4 Event Driven，而不是轮询脚本

面向 AI 的钱包需要一等公民级事件流，而不是只有 CLI 定时跑：

- `utxo.expiring`
- `utxo.window_opened`
- `renewal.succeeded`
- `renewal.failed`
- `policy.fee_above_cap`
- `wallet.sync_lag`
- `reap_risk.elevated`

### 4.5 Audit Native

每一个 agent 动作都应该天然带：

- `operation_id`
- `idempotency_key`
- `requested_by`
- `capability_id`
- `policy_version`
- `decision_reason`
- `simulation_hash`

对 AI 钱包来说，审计不是附属功能，而是核心功能。

### 4.6 One Core, Many Frontends

未来实现上应坚持：

- 钱包核心能力只实现一套
- CLI 不直接绕开核心逻辑
- 程序接口不是 CLI 的附属品，CLI 也不是程序接口的例外分支

推荐关系是：

- 核心域模型：wallet / signer / policy / audit
- 标准程序接口：gRPC
- 可选程序网关：HTTP/JSON
- 人类入口：CLI，对标准程序接口做薄封装

这样一来：

- 人类和 AI 看到的是同一套操作语义
- 审计、权限、策略、锁定、预演都能统一复用
- 后续加 GUI 也不会破坏内核边界

---

## 5. 推荐架构
```text
Human Operator (CLI)      AI Agents / Services
         |                         |
         +-----------+-------------+
                     |
                     v
              Agent Wallet Gateway
                 |- CLI Adapter
                 |- gRPC / HTTP API
                 |- Capability Service
                 |- Policy Engine
                 |- Expiry/Renew Planner
                 |- UTXO Reservation Manager
                 |- Audit Log
                 |
                 +--> Watch-Only Wallet State (xpub, utxo, tx, expiry, labels)
                 |
                 +--> Signer Service / HSM / TEE / KMS-backed enclave
                          |- generate seed
                          |- derive child keys
                          |- sign PSBT / sign intent
                          |- never expose seed to agent
```

### 5.1 交互层划分

`CLI`

- 面向人类操作员
- 用于查询、预演、审批、续期、恢复、巡检
- 应尽量调用标准程序接口，而不是重新写一套逻辑

`gRPC / HTTP API`

- 面向 AI agent、后端服务、运维自动化、未来 GUI
- 应该是长期稳定的主接口
- 应提供结构化错误、幂等语义、操作状态查询

`PSBT`

- 不是独立产品界面
- 而是规划层与签名层之间的标准交换格式

### 5.2 为什么 CLI 也应纳入统一接口体系

如果 CLI 直接调用一套特殊内部逻辑，而 AI 走另一套 RPC，最后一定会出现：

- 人类能做、AI 不能做
- AI 能预演、人类不能预演
- 审计字段 CLI 缺失
- capability 约束只在 AI 路径生效

这会让钱包系统快速失控。

所以更合理的做法是：

- CLI 只是“面向人类的调用器”
- 程序接口才是“能力定义的源头”
- 两者共享 plan / sign / publish / audit / policy 语义

### 5.3 各层职责

`Agent Wallet Gateway`

- 对外暴露 gRPC 或 HTTP API
- 对 CLI 提供统一适配层
- 做 capability 校验
- 做 policy 检查
- 生成 plan / PSBT / renewal proposal
- 维护 operation 和审计记录

`Watch-Only Wallet State`

- 保存账户、地址、UTXO、tx、expiry、policy、labels、audit
- 默认不保存可直接签名的明文私钥
- 供 agent 查询和规划

`Signer Service`

- 持有根密钥材料
- 支持本地 enclave、远程 signer、HSM、KMS 包装
- 只做小而确定的签名动作

---

## 6. 钱包创建与密钥保存建议

### 6.1 推荐的三种创建模式

### 模式 A：`dev_local`

只用于开发和 simnet/regtest。

流程：

1. `CreateWallet` 在本地进程内生成 seed
2. `wallet.db` 本地保存加密后的根密钥材料
3. 用 passphrase 解锁签名

这个模式可直接复用当前 `obtcwallet` 的创建逻辑，但不建议作为生产默认模式。

### 模式 B：`remote_signer`

生产默认模式。

流程：

1. signer 端生成 seed，seed 永不出 enclave/HSM
2. wallet gateway 只拿到：
   - `wallet_id`
   - `master_fingerprint`
   - `account_xpub` / scope xpub
   - signer capability handle
3. `obtcwallet` 以 watch-only 方式管理地址、UTXO、expiry、policy
4. 所有签名都走 `SignPlan` / `SignPsbt`

这是最适合 AI agent 的模式，因为可以天然做到：

- agent 不接触 seed
- planner 和 signer 分离
- 能做多 agent 最小授权

### 模式 C：`recover_once`

用于灾备恢复。

流程：

1. 通过一次性安全引导通道提交 mnemonic / seed
2. signer 导入并立即封存
3. 钱包只保留恢复结果和审计记录
4. recovery material 不进入日志、提示词、环境变量、长期配置文件

---

### 6.2 密钥保存原则

### 不建议 AI 直接保存的东西

- mnemonic
- raw seed
- wallet private passphrase
- 根私钥导出文本
- 长期有效的 signer 管理口令

这些都不应该出现在：

- prompt
- agent memory
- 普通数据库字段
- `.env`
- job payload
- 普通日志

### 推荐保存方式

`watch-only / planner 侧`

- xpub / account xpub
- 地址、UTXO、交易、expiry 信息
- capability、policy、审计记录
- 与 signer 的引用关系

`signer 侧`

- seed 或 root key
- 使用 KEK/DEK 包装
- KEK 由 KMS/HSM/TEE 提供
- 支持轮换 wrapping key，不要求重建整个钱包

### 对当前仓库的直接启发

当前 `waddrmgr` 已有“主密钥 -> crypto key -> 账户/脚本/根 HD key”分层加密模型，这很好，说明可以继续沿用“多层包装”的思想。

但对生产 AI 场景，建议把“根 HD 私钥加密后保存在本地 wallet.db”降级为兼容模式，而不是默认模式。

---

### 6.3 agent 账户隔离建议

未来不要让多个 agent 共用同一个默认账户。

建议为每个 agent 建立独立账户或独立 scope：

- `treasury`
- `renewal-agent`
- `payments-agent`
- `market-agent`
- `recovery-agent`

至少做到：

- 地址空间隔离
- 预算隔离
- capability 隔离
- 审计隔离

对 OBTC 来说，这一点尤其重要，因为不同 agent 面对的是不同的 expiry 风险和资金时效目标。

---

## 7. 推荐新增的接口层

不建议继续把 AI 能力堆进 Legacy JSON-RPC。

建议新增：

- 一个新的 gRPC 服务：`AgentWalletService`
- 必要时再在其上面提供 HTTP/JSON gateway

同时建议把命令行层明确纳入设计：

- Legacy JSON-RPC：兼容层，逐步减少新增能力
- gRPC：主程序接口
- HTTP/JSON：可选网关，服务外部系统
- CLI：人类入口，调用主程序接口

### 7.0 接口面建议

| 入口 | 目标用户 | 定位 |
| --- | --- | --- |
| Legacy JSON-RPC | 旧脚本、兼容调用方 | 兼容层 |
| gRPC | AI agent、后端服务、未来 GUI | 主接口 |
| HTTP/JSON gateway | Web 后端、轻量集成方 | 适配层 |
| CLI | 人类操作员 | 一等入口，但应是薄封装 |
| PSBT | signer 交换面 | 标准中间格式 |

### 7.1 生命周期接口

| 方法 | 作用 | 说明 |
| --- | --- | --- |
| `CreateWatchOnlyWallet` | 创建观察钱包 | 只导入 xpub / account xpub |
| `AttachSigner` | 绑定 signer | 建立 wallet 与 signer 的映射 |
| `RecoverWallet` | 一次性恢复 | 用于灾备恢复 |
| `CreateAgentAccount` | 创建 agent 专属账户 | 最小授权的基础 |
| `ExportWatchOnly` | 导出给下游 agent | 用于只观察或只规划 |

### 7.2 观察接口

| 方法 | 作用 | 说明 |
| --- | --- | --- |
| `GetWalletState` | 当前钱包全局状态 | 网络、高度、同步状态、账户摘要，以及当前 signer backend 模式 |
| `ListUtxos` | 查询 UTXO | 支持账户、标签、锁状态、expiry 过滤 |
| `GetExpiryRisk` | 查询到期风险 | AI 的一等公民接口 |
| `StreamWalletEvents` | 事件流 | 支持 expiry / renew / sync / fee / policy 事件 |

`GetExpiryRisk` 不应只是 `obtc.getexpiry` 的简单换皮。它建议至少返回：

- `create_height`
- `expiry_height`
- `blocks_to_expiry`
- `days_to_expiry`
- `status`
- `dust_risk`
- `projected_refund_sat`
- `renew_recommended`
- `latest_safe_action_height`
- `policy_action`

### 7.3 规划接口

| 方法 | 作用 | 说明 |
| --- | --- | --- |
| `PreviewRenewal` | 预演续期 | 不签名、不广播 |
| `CreateTransferPlan` | 创建转账方案 | plan-first |
| `CreatePsbtPlan` | 生成 PSBT 方案 | 复用 `FundPsbt` 能力 |
| `ReserveUtxos` | 预留输入 | 避免多 agent 冲突 |
| `ReleaseReservation` | 释放预留 | 失败回滚 |

### 7.4 执行接口

| 方法 | 作用 | 说明 |
| --- | --- | --- |
| `SubmitRenewal` | 提交预演过的续期操作 | 当前 phase-1 兼容包装器，内部顺序调用 `SignPsbt` + `PublishTransaction` |
| `OpenSignerSession` | 打开本地 signer 会话 | 当前 phase-1.5 已落地，仅 local signer backend 可用 |
| `CloseSignerSession` | 关闭 signer 会话 | 当前 phase-1.5 已落地，主动结束本地 signer 会话 |
| `SignPlan` | 请求 signer 对计划签名 | capability + policy + session |
| `SignPsbt` | 对 PSBT 签名 | 当前 phase-1 已落地，基于 `operation_id` 和持久化 unsigned PSBT |
| `FinalizePlan` | 完成交易装配 | 可选步骤 |
| `PublishTransaction` | 广播交易 | 当前 phase-1 已落地，支持复用已签名 PSBT 或直接接收外部 signed PSBT；phase-1.5 增加 capability 校验 |

### 7.5 策略接口

| 方法 | 作用 | 说明 |
| --- | --- | --- |
| `UpsertRenewalPolicy` | 配置续期策略 | 持久化，不应只停留在进程内 |
| `UpsertSpendPolicy` | 配置花费策略 | 限额、白名单、费率上限 |
| `IssueCapability` | 发放权限令牌 | 当前 phase-1.5 已落地，给具体 agent/CLI 会话 |
| `RevokeCapability` | 撤销权限令牌 | 当前 phase-1.5 已落地，发现异常时快速止血 |

### 7.6 审计接口

| 方法 | 作用 | 说明 |
| --- | --- | --- |
| `GetOperation` | 查询单个操作 | 计划、签名、广播全链路 |
| `ListOperations` | 列出操作 | 当前 phase-1 已落地，支持 wallet/state/kind/creator 过滤和顺序控制 |
| `GetDecisionLog` | 查询策略决策依据 | 便于回放 agent 行为 |

---

### 7.7 核心资源模型

如果这份文档要落成工程实现，接口层必须先定义“资源”而不是只定义“命令”。

建议最少先固定下面这些资源：

| 资源 | 主键 | 用途 |
| --- | --- | --- |
| `wallet` | `wallet_id` | 钱包根容器 |
| `account` | `account_id` | 人类/agent 的逻辑隔离单元 |
| `capability` | `capability_id` | 细粒度授权令牌 |
| `policy` | `policy_id` + `version` | 风险、预算、费率、脚本策略 |
| `reservation` | `reservation_id` | UTXO 预留/租约 |
| `plan` | `plan_id` | 规划出的可执行方案 |
| `operation` | `operation_id` | 执行态对象，承载签名/广播/确认流程 |
| `decision_log` | `decision_id` | 策略引擎决策证据 |
| `event` | `event_id` | 钱包事件流中的记录 |

其中建议这样区分：

- `plan`：还没有动用私钥，不应视为资金已执行
- `operation`：进入执行态，开始承载签名、广播、确认和失败信息
- `reservation`：解决多 agent / 多 CLI 会话的输入冲突

### 7.8 操作状态机

建议所有执行类动作都统一为一个状态机，而不是每个 API 自己发明状态字段。

推荐最小状态集合：

```text
DRAFT
  -> RESERVED
  -> POLICY_REJECTED
  -> CANCELLED

RESERVED
  -> SIGNING
  -> CANCELLED
  -> EXPIRED

SIGNING
  -> SIGNED
  -> FAILED
  -> CANCELLED

SIGNED
  -> PUBLISHING
  -> CANCELLED

PUBLISHING
  -> PUBLISHED
  -> FAILED

PUBLISHED
  -> CONFIRMED
  -> REORGED
  -> FAILED

REORGED
  -> PUBLISHED
  -> FAILED
```

工程上有两个好处：

- CLI、AI、后端服务都能读同一套状态
- 审计和重试逻辑可以统一挂在 `operation` 上

### 7.9 通用响应信封

不建议每个方法各自定义完全不同的顶层响应风格。

推荐统一携带以下元数据：

```json
{
  "request_id": "req_001",
  "operation_id": "op_001",
  "idempotency_key": "renew-001",
  "policy_verdict": "allow",
  "warnings": [],
  "result": {}
}
```

说明：

- `request_id`：链路追踪
- `operation_id`：后续查询执行状态
- `idempotency_key`：重试去重
- `policy_verdict`：让调用方知道是系统允许、拒绝还是仅预演
- `warnings`：给 CLI 和 AI 共用的提示通道

### 7.10 错误模型

建议 gRPC、HTTP、CLI 三层共用一份逻辑错误码语义。

推荐最小错误码集合：

| 错误码 | 含义 | 典型场景 |
| --- | --- | --- |
| `INVALID_ARGUMENT` | 请求参数非法 | outpoint 格式错误、窗口非法 |
| `FAILED_PRECONDITION` | 当前状态不允许 | 钱包未加载、未同步、未绑定 signer |
| `UNAUTHORIZED` | capability 不足 | agent 越权 |
| `POLICY_DENIED` | 策略拒绝 | 费率超限、金额超预算 |
| `LOCK_REQUIRED` | 需要 signer 解锁或 session | 当前 signer 不可用 |
| `RESERVATION_CONFLICT` | UTXO 已被其他会话预留 | 多 agent 冲突 |
| `CHAIN_UNSYNCED` | 链状态不可用 | 未同步到安全高度 |
| `NETWORK_MISMATCH` | 网络参数不匹配 | OBTC/Testnet/Regtest 错配 |
| `NOT_FOUND` | 资源不存在 | plan / operation / account 不存在 |
| `INTERNAL` | 内部错误 | DB、序列化、未知异常 |

建议特别避免把所有失败都压成字符串错误，因为 AI 和 CLI 都需要机器可读的失败分类。

---

## 8. 推荐的最小请求语义与 proto 草案

### 8.1 `PreviewRenewal`

```json
{
  "wallet_id": "w_001",
  "account_id": "renewal-agent",
  "outpoints": ["txid:vout"],
  "target_policy": {
    "script_rotation": "new_address",
    "max_fee_rate_sat_per_kb": 5000
  },
  "idempotency_key": "renew-preview-20260308-001"
}
```

响应建议包含：

```json
{
  "plan_id": "plan_001",
  "mode": "renewal",
  "inputs": 1,
  "outputs": 1,
  "estimated_fee_sat": 1200,
  "projected_new_expiry_height": 1312880,
  "warnings": [],
  "policy_verdict": "allow"
}
```

### 8.2 `IssueCapability`

```json
{
  "wallet_id": "w_001",
  "principal": "agent:renewal-bot",
  "permissions": [
    "observe.expiry",
    "plan.renewal",
    "sign.renewal",
    "publish.own_plans"
  ],
  "constraints": {
    "account_ids": ["renewal-agent"],
    "max_amount_sat_per_day": 500000000,
    "max_fee_rate_sat_per_kb": 5000
  },
  "ttl_seconds": 3600
}
```

### 8.3 `SignPlan`

```json
{
  "plan_id": "plan_001",
  "capability_id": "cap_renew_01",
  "session_reason": "scheduled renewal run",
  "idempotency_key": "sign-plan-001"
}
```

### 8.4 `AgentWalletService` proto 草案

截至当前仓库状态，最小可运行版本已经落到：

- `rpc/agentwallet/agentwallet.proto`
- `rpc/rpcserver/agentwallet_server.go`

当前已实现的方法是：

- `GetWalletState`
- `ListUtxos`
- `GetExpiryRisk`
- `IssueCapability`
- `RevokeCapability`
- `OpenSignerSession`
- `GetSignerSession`
- `CloseSignerSession`
- `PreviewRenewal`
- `SignPsbt`
- `PublishTransaction`
- `SubmitRenewal`
- `ReserveUtxos`
- `ReleaseReservation`
- `GetOperation`
- `ListOperations`

其中当前真正形成闭环的执行路径是：

- `GetExpiryRisk` -> `PreviewRenewal` -> `SignPsbt` -> `PublishTransaction`
- `GetExpiryRisk` -> `PreviewRenewal` -> `SubmitRenewal`

也就是说，当前版本已经支持 agent 或 CLI 先拿到 unsigned PSBT 和风险视图，再基于 `operation_id` 分步签名和广播；`SubmitRenewal` 继续保留为兼容包装器。当前 `PublishTransaction` 也支持直接接收外部 signer 返回的 signed PSBT，但仍然要求绑定已有 `operation_id`，不会脱离预演上下文自由提交。

另外，当前 `operation` 已经开始携带基础审计元数据，包括：

- `creator_principal`
- `creator_capability_id`
- `create_idempotency_key`
- `history[]`
- `latest_policy_snapshot`
- `latest_signer_proof`
- `decision_log[]`

这意味着当前版本已经不只记录“状态怎么变了”，还会记录：

- 预演/签名/广播时看到的 policy snapshot
- 当时采用的 verdict、warning、tip height、min confirmations
- 由哪个 signer backend、哪个 signer session 产出的 signer proof
- unsigned PSBT / signed PSBT / signed tx 的摘要指纹

也就是说，phase-1 现在已经有最小可用的 decision log / signer proof 闭环；但它仍然不是最终形态，因为 attestation、HSM key handle、设备证明和远端 signer 回执还没有纳入模型。

当前 phase-1.5 还新增了最小 capability / signer session 约束：

- `IssueCapability` / `RevokeCapability` 把授权对象持久化到 walletdb
- `OpenSignerSession` / `CloseSignerSession` 把本地 signer 从“裸 `wallet.Unlock`”收敛成显式会话
- `SignPsbt` / `SubmitRenewal` 现在要求 `capability_id + signer_session_id`
- `PublishTransaction` 现在要求 `capability_id`，并可选绑定 `signer_session_id`
- signer runtime 已经被抽象成 backend interface，当前已有 `local`、`publish_only`、`remote` 三种 backend；`remote` 已接入最小可用的 gRPC transport

其中当前 backend 语义是：

- `local`：可打开本地 signer session，并在 session 内完成 `SignPsbt`
- `publish_only`：不提供本地 signer session，只接受外部 signer 返回的 signed PSBT，再通过 `PublishTransaction` 广播
- `remote`：当前可通过 `RemoteSignerService` gRPC transport 打开远端 signer session、远端完成 `FinalizePsbt`，也支持通过 `authorization` bearer token 做最小认证
- `GetWalletState` 已会返回 `signer_backend` 元数据，供 AI/CLI 判断当前应走哪条执行路径

但要明确一点：底层本地 signer 目前仍然复用 wallet 的全局 unlock 语义，因此当前实现只支持一个 active local signer session。这是兼容性过渡层，不是最终的多 signer backend 形态。

下面这段仍然保留为“更长线的目标草案”，用于约束后续 capability / signer / event stream / publish path 的演进方向。

```proto
syntax = "proto3";

package agentwalletrpc;

service AgentWalletService {
  rpc GetWalletState(GetWalletStateRequest) returns (GetWalletStateResponse);
  rpc ListUtxos(ListUtxosRequest) returns (ListUtxosResponse);
  rpc GetExpiryRisk(GetExpiryRiskRequest) returns (GetExpiryRiskResponse);
  rpc StreamWalletEvents(StreamWalletEventsRequest) returns (stream WalletEvent);

  rpc PreviewRenewal(PreviewRenewalRequest) returns (PreviewRenewalResponse);
  rpc CreateTransferPlan(CreateTransferPlanRequest) returns (CreateTransferPlanResponse);
  rpc CreatePsbtPlan(CreatePsbtPlanRequest) returns (CreatePsbtPlanResponse);

  rpc ReserveUtxos(ReserveUtxosRequest) returns (ReserveUtxosResponse);
  rpc ReleaseReservation(ReleaseReservationRequest) returns (ReleaseReservationResponse);

  rpc SignPlan(SignPlanRequest) returns (SignPlanResponse);
  rpc SignPsbt(SignPsbtRequest) returns (SignPsbtResponse);
  rpc PublishTransaction(PublishTransactionRequest) returns (PublishTransactionResponse);

  rpc UpsertRenewalPolicy(UpsertRenewalPolicyRequest) returns (UpsertRenewalPolicyResponse);
  rpc UpsertSpendPolicy(UpsertSpendPolicyRequest) returns (UpsertSpendPolicyResponse);
  rpc IssueCapability(IssueCapabilityRequest) returns (IssueCapabilityResponse);
  rpc RevokeCapability(RevokeCapabilityRequest) returns (RevokeCapabilityResponse);

  rpc GetOperation(GetOperationRequest) returns (GetOperationResponse);
  rpc ListOperations(ListOperationsRequest) returns (ListOperationsResponse);
}

message RequestMeta {
  string request_id = 1;
  string idempotency_key = 2;
  string principal = 3;
  string capability_id = 4;
}

message OperationMeta {
  string operation_id = 1;
  string plan_id = 2;
  string policy_verdict = 3;
  repeated string warnings = 4;
}

message UtxoRef {
  string txid = 1;
  uint32 vout = 2;
}

message ExpiryRisk {
  UtxoRef outpoint = 1;
  int64 amount_sat = 2;
  int32 create_height = 3;
  int32 expiry_height = 4;
  int32 blocks_to_expiry = 5;
  int32 days_to_expiry = 6;
  string status = 7;
  bool dust_risk = 8;
  int64 projected_refund_sat = 9;
  bool renew_recommended = 10;
  int32 latest_safe_action_height = 11;
  string policy_action = 12;
}

message PreviewRenewalRequest {
  RequestMeta meta = 1;
  string wallet_id = 2;
  string account_id = 3;
  repeated UtxoRef outpoints = 4;
  bool dry_run = 5;
  int64 max_fee_rate_sat_per_kb = 6;
  string target_script_strategy = 7;
}

message PreviewRenewalResponse {
  OperationMeta meta = 1;
  bytes unsigned_psbt = 2;
  int64 estimated_fee_sat = 3;
  int32 projected_new_expiry_height = 4;
}
```

这份 proto 草案的设计重点是：

- 先统一 `RequestMeta` / `OperationMeta`
- 先把 `plan` 和 `operation` 做成一等公民
- 先把 `ExpiryRisk` 变成结构化模型，而不是零散字段拼装

### 8.5 CLI 与 API 的映射建议

为了让 CLI 和程序接口共用一套能力模型，建议明确“命令只是接口的外衣”。

| CLI 命令 | 建议映射 API | 说明 |
| --- | --- | --- |
| `renewall --dry-run ...` | `GetWalletState` + `GetExpiryRisk` | 当前实现先做候选筛选并输出 outpoint，不触发签名或广播 |
| `renewall ...` | `GetWalletState` + `GetExpiryRisk` + `IssueCapability` + `OpenSignerSession` + `PreviewRenewal` + `SubmitRenewal` | 当前 CLI 已切到 agent gRPC 路径，`SubmitRenewal` 作为兼容包装器内部继续复用 `SignPsbt` + `PublishTransaction` 语义 |
| `walletpassphrase` | 不直接暴露为长期主接口 | 未来更适合 session/capability |
| `walletlock` | `CloseSignerSession` 或 `RevokeCapability` 的兼容实现 | 收敛到 signer/session 模型 |
| `getnewaddress` | `NextAddress` 或未来 `CreateAddressIntent` | 保持兼容 |
| `obtc.getexpiry` | `GetExpiryRisk` | 已可继续收敛到 agent 语义 |
| `obtc.renew` | `PreviewRenewal` + `SignPsbt` + `PublishTransaction` | legacy 语义仍存在，但主执行路径已不再是唯一入口 |

这样做的含义不是删除 CLI，而是让 CLI 变成：

- 更稳定
- 更容易审计
- 更容易被测试
- 与 AI 行为完全对齐

### 8.6 为什么不建议继续扩张 Legacy JSON-RPC

Legacy JSON-RPC 仍然有价值，但更适合：

- 兼容旧脚本
- 兼容现有人工操作流
- 作为过渡层

不适合继续承载的东西包括：

- capability
- reservation
- operation 状态机
- 事件流
- signer 抽象
- 策略版本化

因此建议的原则是：

- `Legacy JSON-RPC`：只保兼容和必要桥接
- `gRPC`：承载新的语义模型
- `CLI`：优先调用 gRPC，而不是继续扩张 legacy 方法集合

---

## 9. 为什么要优先走 PSBT-first

`obtcwallet` 当前已经有 `FundPsbt` / `FinalizePsbt`，这对 AI 场景很关键。

PSBT-first 的好处：

- 规划和签名天然解耦
- 支持多方协作
- signer 可以更小、更封闭
- 更容易做 dry-run、审计、回放
- 更适合未来接 HSM / 远程 signer / 多签系统

因此建议：

- 对 AI 钱包，`Plan -> PSBT -> Sign -> Publish` 作为主路径
- `obtc.renew` 这种“一步到位广播”的接口保留给兼容层或人工场景

---

## 10. OBTC 特有的 AI 能力，不应只做“转账钱包”

如果未来默认面向 AI，钱包不应停留在“创建地址、发交易、查余额”。

下面这些能力更符合 OBTC 的长期方向。

### 10.1 Expiry Treasury Scheduler

钱包持续维护：

- 哪些 UTXO 正在接近 expiry
- 哪些输出存在 dust risk
- 哪些账户在未来 N 天需要多少 renewal budget

这本质上是一个时间风险调度器，而不只是余额系统。

### 10.2 Autonomous Renewal Desk

一个专门的 `renewal-agent` 可以：

- 按窗口批量预演 renewal
- 在预算和费率限制下自动续期
- 在费率过高时延期
- 失败时退避
- 输出结构化审计和异常事件

这比“cron + CLI”更接近真正的 agent wallet。

### 10.3 Time-Bounded Subwallet

由于 OBTC 有 expiry，未来可以给 agent 分配“带生命周期预算”的子钱包：

- 某任务 agent 获得一个专用账户
- 预算有期限、有续期策略、有自动回收策略
- 任务结束后 capability 自动失效

这非常适合：

- 交易机器人
- 市场做市 agent
- 计算任务支付 agent
- DAO / treasury 子任务执行 agent

### 10.4 Explainable Treasury

人类运营者未来不会只问“为什么花了这笔钱”，还会问：

- 为什么这批 UTXO 没续期？
- 为什么这个 agent 当时选择合并而不是续期？
- 为什么在这个费率下仍然执行？

因此钱包层应内建：

- 决策依据
- 使用的 policy 版本
- 当时的链状态
- 当时的 expiry 风险快照

### 10.5 Multi-Agent Capital Segmentation

未来可能不是一个 agent 管一个钱包，而是：

- treasury-agent
- renewal-agent
- ops-agent
- market-agent
- recovery-agent

它们共享一套钱包系统，但不共享同一个解锁态和同一份能力集合。

### 10.6 Machine-Readable Recovery and Governance

如果 OBTC 真要走向 agent 生态，恢复和治理也应该机器可读：

- 谁能冻结 capability
- 谁能切换 signer
- 谁能触发恢复模式
- 谁能提升费率上限

这类治理接口建议直接纳入 wallet API 设计，而不是以后散落在脚本里。

---

## 11. 对 `obtcwallet` 的落地建议

### 11.0 当前代码复用矩阵

这份设计不是从零开始，下面这些能力可以直接复用或半复用：

| 目标能力 | 当前可复用实现 | 说明 |
| --- | --- | --- |
| 钱包创建/加载 | `WalletLoaderService` | 现有 loader 可保留，但生产模式要增加 watch-only / remote signer |
| 地址生成 | `NextAddress` / `wallet.NewAddress` | 可直接复用 |
| 到期基础计算 | `wallet.BuildExpiryInfo` | 应改为链参数驱动 |
| 手动续期执行 | `SendOutputsWithInput` | 仍可作为兼容包装器 `SubmitRenewal` 的底层执行器 |
| 自动续期策略骨架 | `ConfigureAutoRenew` / `wallet/autorenew.go` | 需要补持久化和事件化 |
| expiry policy 解析 | `wallet/ResolveExpiryPolicy` | 已支持 source file / network override / fallback |
| PSBT 规划 | `FundPsbt` | 可直接复用为计划层底座 |
| PSBT 最终签名 | `FinalizePsbt` | 可直接复用到 signer path |
| UTXO reservation / lease | `LeaseOutput` / `ReleaseOutput` / `ListLeasedOutputs` | 已适合做跨进程 TTL reservation 底座 |
| Agent gRPC phase-1 | `rpc/agentwallet/agentwallet.proto` + `rpc/rpcserver/agentwallet_server.go` | 已有可运行的 agent/CLI 共享程序接口雏形 |
| 钱包加解锁 | `Unlock` / `Lock` | 可保兼容，但不是未来主授权模型 |

### 11.1 当前代码里需要特别补强的地方

除了前面提到的硬编码 expiry 参数，工程上还有几处需要明确补强：

1. `LockOutpoint` / `UnlockOutpoint` 仍然只是进程内锁；agent 路径应统一基于 `LeaseOutput` / `ReleaseOutput`，当前 phase-1 已按这个方向实现。
2. `ConfigureAutoRenew` 当前是进程内配置，不是持久化策略对象。
3. `obtc.getexpiry` / `obtc.renew` 仍然保留 legacy RPC 语义；但 `cmd/renewall` 已经切到 agent gRPC 路径，当前通过 `GetWalletState` / `GetExpiryRisk` / `IssueCapability` / `OpenSignerSession` / `PreviewRenewal` / `SubmitRenewal` 执行续期，并继承 `operation` / `capability` / `signer_session` / `decision_log` / `latest_signer_proof` 这套新审计语义。
4. 当前 signer runtime 已经被抽成 backend interface，并且已有 `local`、`publish_only`、`remote` 三种 backend；其中 `remote` 现已支持最小可用的 gRPC transport，可通过 `RemoteSignerService` 把 signer session open/close 和 `FinalizePsbt` 委托给远端 signer / HSM 代理，但 attestation、设备证明、策略回执还没有纳入 signer proof。
5. expiry 参数已经直接链接到 `obtcd` 的 `chaincfg.GetExpiryParams()`；文档前文提到的 runtime source resolver 现在已经是历史阶段，不再是当前主路径。

### 11.2 建议的最小实现顺序

当前 phase-1 已经完成：

1. 定义 `rpc/agentwallet/agentwallet.proto`
2. 落地 `GetWalletState` / `ListUtxos` / `GetExpiryRisk` / `PreviewRenewal`
3. 用 `LeaseOutput` / `ReleaseOutput` 做持久化 `reservation`
4. 让 `PreviewRenewal` 直接复用 `FundPsbt`，返回 unsigned PSBT 和交易摘要
5. 增加 `SubmitRenewal`，基于 `operation_id` 执行并广播已预演的续期交易
6. 把 `operation` / `reservation` 元数据从内存态提升为 walletdb 持久化对象，并在服务启动时恢复
7. 拆出 `SignPsbt` / `PublishTransaction`，让本地 signer 和外部 signer 都能走 preview-first 执行链路
8. 引入最小 capability / signer session，对 `SignPsbt` / `SubmitRenewal` / `PublishTransaction` 增加程序化授权约束
9. 把 signer runtime 抽象成 backend interface，避免 session/open/close/sign 继续硬编码在 agent server 中

下一步更合理的顺序是：

1. 在 remote signer transport 之上增加更强的 signer 证据模型，例如 HSM key handle、attestation、设备证明、远端 signer receipt
2. 把 `decision_log` 从当前字符串 `reasons[]` 升级成更强的结构化策略证据对象
3. 继续把其他 CLI/自动化入口迁到 agent gRPC，而不是只迁 `renewall`
4. 最后再考虑把 legacy `obtc.renew` 重定向到新执行链路

### 11.3 CLI 改造策略

CLI 不建议一次性重写，但 `cmd/renewall` 已经完成第一条主路径迁移。

更稳妥的办法是：

- 第一步：选一个高价值 CLI 先切到 agent gRPC，验证 capability / signer session / operation 审计链路
- 第二步：保留 CLI 交互表面不变，但让内部默认走新 gRPC
- 第三步：等新接口稳定后，把其他 CLI 的主要逻辑迁出 legacy JSON-RPC

这样可以把迁移风险压到最低。

### 阶段 1：先补“AI 兼容层”而不是大改共识相关代码

建议先做：

1. 新增 `AgentWalletService` 文档和 proto 草案
2. 把 expiry 参数改为链感知，而不是本地硬编码
3. 把 `obtc.getexpiry` 拆成：
   - `GetExpiryRisk`
   - `PreviewRenewal`
   - `SubmitRenewal`
4. 为所有执行类接口补：
   - `operation_id`
   - `idempotency_key`
   - `policy_verdict`
   - `dry_run`

### 阶段 2：引入 capability 和 reservation

建议新增：

- capability 表
- UTXO reservation / lease
- 按账户的 agent 隔离
- operation audit log

### 阶段 3：把 signer 抽离

建议把当前“全局 unlock 后直接签名”的路径抽象成 signer backend：

- local signer
- remote signer
- HSM signer
- TEE signer

当前本地 signer 可以继续兼容，但不应是未来默认架构。

### 阶段 4：做事件流和策略持久化

当前 `autorenew` 更像进程内调度器，且配置并不天然持久化。

面向 agent 的版本应增加：

- 持久化 renewal policy
- scheduler execution log
- event stream / webhook
- retry / backoff / dead letter queue

---

## 12. 一句话结论

OBTC 的钱包，不应该拆成“AI 钱包”和“人类钱包”两套系统，也不应该只是“把人类钱包 RPC 给 AI 调用”。

它应该演进成：

**以 expiry 风险管理为核心、以 capability 为授权单位、以 PSBT 和 intent 为操作语义、以 signer 隔离和审计追踪为安全边界，同时向 AI 和 CLI 人类用户提供统一能力模型的 wallet system。**

如果只做“余额 + 地址 + sendtoaddress”，那只是把传统钱包接到了 AI 上；  
如果做到“观察、规划、授权、签名、续期、审计、恢复”这整套分层，并让 CLI 与程序接口共用同一套核心语义，才真正是适合 OBTC 的钱包形态。
