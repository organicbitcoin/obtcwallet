# OBTCWallet 新人代码阅读指南（截至 2026-03-10）

> 目标读者：刚加入 `obtcwallet` 项目的开发者，尤其是需要同时理解 `btcwallet` 原有骨架与 OBTC 新增能力的人
>
> 目标：帮助你从“知道这个仓库能做什么”，过渡到“知道关键代码在哪、调用链怎么走、哪里是 OBTC 特有逻辑、哪里还是上游 `btcwallet` 基础设施”
>
> 一句话定位：`obtcwallet` 不是链上共识实现，它是 **钱包侧的状态管理、到期风险计算、续期执行与 agent 化接口层**；链侧到期索引、REAP 共识和区块规则由 `obtcd` 负责。

---

## 目录

- [第一章：先建立整体认知](#第一章先建立整体认知)
- [第二章：最近几周这个仓库发生了什么](#第二章最近几周这个仓库发生了什么)
- [第三章：目录和模块地图](#第三章目录和模块地图)
- [第四章：程序是怎么启动起来的](#第四章程序是怎么启动起来的)
- [第五章：钱包核心骨架有哪些](#第五章钱包核心骨架有哪些)
- [第六章：链同步、UTXO、交易构造三条主路径](#第六章链同步utxo交易构造三条主路径)
- [第七章：OBTC 到期模型在钱包侧是怎么落地的](#第七章obtc-到期模型在钱包侧是怎么落地的)
- [第八章：Legacy RPC 扩展是怎么工作的](#第八章legacy-rpc-扩展是怎么工作的)
- [第九章：自动续期调度器是怎么工作的](#第九章自动续期调度器是怎么工作的)
- [第十章：Agent Wallet 是本仓最重要的新架构](#第十章agent-wallet-是本仓最重要的新架构)
- [第十一章：`renewall` CLI 已经切到 agent gRPC 主路径](#第十一章renewall-cli-已经切到-agent-grpc-主路径)
- [第十二章：配置、网络参数与当前实现边界](#第十二章配置网络参数与当前实现边界)
- [第十三章：测试与验证材料怎么看](#第十三章测试与验证材料怎么看)
- [第十四章：推荐阅读路径](#第十四章推荐阅读路径)
- [附录 A：关键文件速查表](#附录-a关键文件速查表)
- [附录 B：新人最容易搞错的点](#附录-b新人最容易搞错的点)
- [附录 C：术语表](#附录-c术语表)

---

## 第一章：先建立整体认知

### 1.1 `obtcwallet` 在 OBTC 体系中的职责

把整个 OBTC 系统拆开看：

- `obtcd` 负责链和共识：
  - OBTC 网络参数
  - ExpiryIndex
  - REAP 交易选择和验证
  - Replay protection
  - Expiry commitment
- `obtcwallet` 负责钱包和操作：
  - 地址/账户/密钥管理
  - UTXO 与交易历史管理
  - 钱包侧 expiry 风险视图
  - “续期”交易构造与广播
  - 自动续期调度
  - 面向 CLI / AI agent / 远程签名器的接口

所以你必须牢记一个边界：

**`obtcwallet` 不维护链级 expiry 状态，也不生成 REAP 系统交易；它只是在钱包侧根据“UTXO 创建高度 + expiry policy”计算风险，并通过普通钱包交易执行续期。**

### 1.2 这个仓库的本质：上游 `btcwallet` 骨架 + OBTC 扩展层

从工程血缘看，`obtcwallet` 仍然是 `btcwallet`：

- 模块名仍是 `github.com/btcsuite/btcwallet`
- 绝大多数地址管理、交易存储、PSBT、链同步基础设施都沿用上游
- OBTC 新增代码主要落在：
  - `wallet/expiry*.go`
  - `wallet/autorenew.go`
  - `rpc/legacyrpc/obtc_methods.go`
  - `cmd/renewall/main.go`
  - `rpc/agentwallet/agentwallet.proto`
  - `rpc/rpcserver/agentwallet_*.go`

因此正确阅读姿势不是“把整个仓库当全新项目”，而是：

1. 先理解 `btcwallet` 原骨架怎么运行
2. 再定位 OBTC 在这个骨架上插了哪几条新路径

### 1.3 `obtcwallet` 与 `obtcd` 的代码依赖关系

`go.mod` 里有一个非常关键的替换：

```go
replace github.com/btcsuite/btcd => ../obtcd
```

这说明：

- `obtcwallet` 编译时实际链接的是本地 `../obtcd`
- 所以钱包可以直接读 `obtcd` 里新增的 `chaincfg` 扩展
- `wallet/expiry_policy.go` 里已经直接调用 `chaincfg.GetExpiryParams()`

但这里有一个后面会反复提到的细节：

- “能直接调用 OBTC chaincfg API”
- 不等于 “当前主程序已经把自己运行在 `ObtcMainNetParams` 上”

当前 `config.go` 已经支持显式切到 `ObtcMainNet / ObtcTestNet / ObtcRegTest`，但默认网络仍然不是 OBTC；如果运行实例没有显式传入 `--obtcmainnet`、`--obtctestnet` 或 `--obtcregtest`，expiry policy 仍可能落到非 OBTC 分支。第十二章会展开讲。

### 1.4 架构总览

可以把这个仓库粗分成 6 层：

```text
┌────────────────────────────────────────────────────┐
│  6. CLI / RPC / Agent API                          │
│     cmd/renewall, rpc/legacyrpc, rpc/rpcserver     │
├────────────────────────────────────────────────────┤
│  5. 钱包业务层                                     │
│     wallet/*.go                                    │
│     交易构造、PSBT、expiry、autorenew              │
├────────────────────────────────────────────────────┤
│  4. 钱包状态子系统                                 │
│     waddrmgr / wtxmgr / walletdb                   │
│     密钥账户、交易历史、数据库命名空间             │
├────────────────────────────────────────────────────┤
│  3. 链后端抽象层                                   │
│     chain/*.go                                     │
│     btcd RPC / neutrino / bitcoind 抽象            │
├────────────────────────────────────────────────────┤
│  2. 网络参数与启动装配层                           │
│     config.go / params.go / netparams / btcwallet.go│
├────────────────────────────────────────────────────┤
│  1. 外部依赖                                       │
│     obtcd(chaincfg) / btcd / gRPC / bbolt          │
└────────────────────────────────────────────────────┘
```

### 1.5 你应该先知道的三条业务主线

这个仓库目前最重要的不是“所有功能”，而是下面三条线：

#### 主线 A：Legacy 钱包续期

```text
obtc.getexpiry / obtc.renew
  -> wallet.BuildExpiryInfo()
  -> wallet.SendOutputsWithInput()
  -> CreateSimpleTx()
  -> PublishTransaction()
```

这是最传统、最直接的路径。

#### 主线 B：自动续期

```text
btcwallet 启动
  -> loadConfig()
  -> autoRenewRuntimeConfigFromOptions()
  -> Wallet.ConfigureAutoRenew()
  -> autoRenewLoop()
  -> buildAutoRenewCandidates()
  -> renewAutoCandidate()
  -> SendOutputsWithInput()
```

这是“钱包进程内调度器”路径。

#### 主线 C：Agent Wallet / PSBT-first 续期

```text
GetExpiryRisk
  -> ReserveUtxos
  -> PreviewRenewal
  -> SignPsbt
  -> PublishTransaction
  -> SubmitRenewal (兼容封装)
```

这是目前最值得投入精力理解的路径。

---

## 第二章：最近几周这个仓库发生了什么

如果只看 2026-02-15 之后的提交，`obtcwallet` 的 OBTC 功能大概按下面顺序推进：

| 日期 | 里程碑 | 关键内容 |
|------|--------|---------|
| 2026-02-15 | Phase 5 基础 | `expiry.go`、文档、git hooks |
| 2026-02-15 | Legacy RPC 接入 | `obtc.getexpiry`、`obtc.renew` |
| 2026-02-15 | 自动续期基础 | `autorenew` policy 骨架 |
| 2026-02-15 | 批量 CLI 基础 | `cmd/renewall` 初版 |
| 2026-02-27 ~ 2026-03-01 | 验证与 CLI 增强 | `phase5-validation.md`、窗口过滤、定时多轮执行 |
| 2026-03-01 | 钱包进程内自动续期 | 定时器、配置项、失败退避、每轮预算上限 |
| 2026-03-08 | Agent Wallet Phase 1 | `AgentWalletService`、capability、signer session、operation 审计 |
| 2026-03-08 | Expiry policy 收敛 | `wallet.ResolveExpiryPolicy()` 与本地测试门槛调整 |
| 2026-03-08 | Preview-first 执行流 | `PreviewRenewal -> SignPsbt -> Publish/Submit` |
| 2026-03-08 | Signer backend 抽象 | local / remote / publish_only 三种 backend |
| 2026-03-09 | 直接链接 `obtcd` chaincfg | expiry policy 改为直接调用 `chaincfg.GetExpiryParams()` |
| 2026-03-09 | 远程签名器落地 | gRPC remote signer transport，`renewall` CLI 切到 agent 路径 |

把这些里程碑压缩成一句话：

**这个仓库已经从“给钱包补两个 OBTC RPC”演进到“围绕 expiry risk 和 renewal flow 搭建 agent 化钱包执行模型”。**

---

## 第三章：目录和模块地图

### 3.1 顶层目录建议你这样看

```text
obtcwallet/
  btcwallet.go                主程序入口
  config.go                   配置解析、网络选择、TLS/监听校验
  rpcserver.go                RPC 服务器装配
  walletsetup.go              创建钱包、模拟钱包、legacy keystore 导入
  params.go                   activeNet 缺省值

  wallet/                     钱包业务核心
  chain/                      链后端抽象
  waddrmgr/                   地址/账户/密钥管理
  wtxmgr/                     交易历史/UTXO/租约
  walletdb/                   数据库存储抽象
  rpc/                        legacy/gRPC/agent/remote signer 协议与服务
  cmd/                        工具命令，最重要的是 renewall
  docs/                       文档与设计说明
```

### 3.2 `wallet/` 目录怎么分块

`wallet/` 是这个仓库最重要的目录，但它本身也分层：

- 基础生命周期：
  - `wallet.go`
  - `loader.go`
  - `chainntfns.go`
  - `rescan.go`
- 交易与 PSBT：
  - `createtx.go`
  - `psbt.go`
  - `signer.go`
  - `utxos.go`
- OBTC 新增：
  - `expiry.go`
  - `expiry_policy.go`
  - `autorenew.go`
- 其他能力：
  - `import.go`
  - `history.go`
  - `multisig.go`
  - `recovery.go`
  - `notifications.go`

新人最容易犯的错误，是一上来只看 `expiry.go`。  
正确做法是先把 `wallet.go`、`loader.go`、`utxos.go`、`psbt.go` 建立上下文，再去看 OBTC 增量。

### 3.3 `rpc/` 目录实际上有 4 套接口

#### 第一套：传统 JSON-RPC

- 目录：`rpc/legacyrpc/`
- 用途：兼容 Bitcoin Core 风格的钱包 RPC
- OBTC 扩展点：`rpc/legacyrpc/obtc_methods.go`

#### 第二套：通用 gRPC 钱包接口

- 协议：`rpc/api.proto`
- 服务：
  - `VersionService`
  - `WalletService`
  - `WalletLoaderService`

这套是 `btcwallet` 传统实验性 gRPC。

#### 第三套：Agent Wallet gRPC

- 协议：`rpc/agentwallet/agentwallet.proto`
- 服务实现：`rpc/rpcserver/agentwallet_server.go`
- 这是 OBTC 当前最重要的新接口层

#### 第四套：Remote Signer gRPC

- 协议：`rpc/remotesigner/remotesigner.proto`
- 作用：给 agent signer backend 提供外部签名器传输层

---

## 第四章：程序是怎么启动起来的

### 4.1 顶层入口：`btcwallet.go`

启动主路径在 `walletMain()`：

1. `loadConfig()`：
   - 解析配置文件和 CLI 参数
   - 选择网络
   - 初始化日志
   - 校验 auto-renew 参数
   - 校验 RPC/TLS/remote signer 配置
2. `wallet.NewLoader(...)`：
   - 创建钱包装载器
3. `startRPCServers(loader)`：
   - 启动 legacy RPC 和/或实验性 gRPC
4. `rpcClientConnectLoop(...)`：
   - 连接链后端
5. `loader.RunAfterLoad(...)` 回调：
   - 绑定 auto-renew
   - 注册 wallet RPC / agent wallet service
6. `loader.OpenExistingWallet(...)`：
   - 打开现有钱包
7. 安装中断处理器并等待退出

### 4.2 两个 `RunAfterLoad` 回调非常关键

`btcwallet.go` 在钱包加载前先注册了两个回调：

#### 回调 1：启用 auto-renew

```text
loadConfig()
  -> autoRenewRuntimeConfigFromOptions()
  -> Wallet.ConfigureAutoRenew()
```

它只做进程内运行时配置，不做持久化。

#### 回调 2：启动钱包服务

```text
startWalletRPCServices()
  -> StartWalletService()
  -> StartAgentWalletServiceWithOptions()
  -> legacyServer.RegisterWallet()
```

也就是说：

- 实验性 gRPC WalletService
- AgentWalletService
- Legacy RPC wallet 绑定

最终都共享同一个 `*wallet.Wallet` 实例。

### 4.3 链连接是异步装配的

`rpcClientConnectLoop()` 会不断尝试连接链后端：

- 如果 `cfg.UseSPV = true`：
  - 启动 `neutrino`
- 否则：
  - 走 `startChainRPC()`，连接 btcd 风格 RPC

连接成功后，它会在 `loader.RunAfterLoad(...)` 中追加一个链同步回调：

```text
wallet.SynchronizeRPC(chainClient)
legacyRPCServer.SetChainServer(chainClient)
```

这说明 wallet 的“打开”和“连链”是两步：

- 可以先打开钱包
- 再异步把链客户端挂上去

### 4.4 创建钱包路径在 `walletsetup.go`

`walletsetup.go` 主要负责：

- `createWallet()`：
  - 交互式创建钱包
  - 选择 passphrase
  - 生成或输入 seed
  - 如有旧版 `keystore`，做导入
- `createSimulationWallet()`：
  - 为 simnet 创建测试钱包
- `networkDir()`：
  - 计算不同网络的数据目录

如果你在排查“为什么钱包文件在这个目录”“为什么 testnet 目录叫 `testnet` 而不是 `testnet3`”，这里就是入口。

---

## 第五章：钱包核心骨架有哪些

### 5.1 `walletdb`：名字空间数据库抽象

`walletdb` 的作用不是“业务逻辑”，而是：

- 统一底层 DB 接口
- 支持 bucket / namespace
- 让多个子系统共享一个钱包 DB 但互不冲突

根本思想是：

**地址管理、交易历史、agent 元数据都放在同一个 DB 里，但每个子系统有自己的 bucket。**

### 5.2 `waddrmgr`：地址、账户、密钥、锁状态

`waddrmgr` 管理：

- HD 根密钥和派生路径
- BIP44/多 scope 账户
- 地址生成与地址归属
- public/private 数据加密
- 锁定/解锁状态

你可以把它理解成“钱包的密钥与账户大脑”。

和本仓阅读直接相关的几个点：

- `wallet.Unlock()` / `wallet.Lock()` 最终都要经过 `waddrmgr`
- `CurrentAddress()` / `NewAddress()` / `NewChangeAddress()` 也都建立在它之上
- local signer backend 本质上就是借助 `waddrmgr` 的 unlock 机制来完成会话化签名

### 5.3 `wtxmgr`：交易历史、UTXO、租约

`wtxmgr` 管理：

- 相关交易记录
- credit/debit
- 未花费输出索引
- 已花费跟踪
- 重组时回滚
- output lease

对 OBTC 新功能最重要的是：

- `wallet.UnspentOutputs()` 最终是从这里拿 UTXO
- `LeaseOutput()` / `ReleaseOutput()` 的持久化锁也在这一层
- `AgentWalletService.ReserveUtxos()` 就是构建在这套 lease 机制之上

### 5.4 `wallet.Wallet`：协调器，不是纯数据对象

`wallet.Wallet` 不是一个普通 struct，而是一个 actor 风格的协调器。

它内部至少有几类状态：

- 数据存储句柄：
  - `db`
  - `Manager`
  - `TxStore`
- 链同步相关：
  - `chainClient`
  - rescan channels
  - notification server
- 并发控制相关：
  - `createTxRequests`
  - `unlockRequests`
  - `lockRequests`
  - `holdUnlockRequests`
- 进程生命周期：
  - `started`
  - `quit`
  - `wg`
- OBTC 新增：
  - `autoRenewCfg`
  - `autoRenewConfigured`
  - `autoRenewLoopRunning`
  - `autoRenewNextAllowedRun`

所以从阅读角度要把 `Wallet` 当成：

**一个持有数据库句柄、链客户端、goroutine、channel 和运行时策略的“钱包运行时内核”。**

### 5.5 这个钱包内核有哪几个关键 goroutine

#### `txCreator()`

用途：

- 串行化交易创建
- 防止多个并发请求选到同一批 UTXO
- 在 coin selection 和签名期间暂时持有 unlock

这就是为什么 `CreateSimpleTx()` 不直接干活，而是把请求扔进 `createTxRequests`。

#### `walletLocker()`

用途：

- 串行化 unlock / lock / hold unlock
- 处理解锁超时
- 处理 passphrase 变更

local signer backend 的“一次 signer session = 一次临时 unlock”就是建立在这条 goroutine 上。

#### `handleChainNotifications()`

用途：

- 处理链后端发来的 block / tx / rescan 通知
- 更新 `SyncedTo`
- 记录相关交易
- 发出钱包通知

#### `rescanBatchHandler()` / `rescanRPCHandler()`

用途：

- 管理 rescan 请求聚合和实际 RPC 调用

#### `autoRenewLoop()`

用途：

- 周期性筛选 expiring UTXO
- 尝试自动续期
- 应用失败退避

---

## 第六章：链同步、UTXO、交易构造三条主路径

### 6.1 链抽象：`chain.Interface`

`chain/interface.go` 定义了钱包依赖的链后端抽象：

- `GetBestBlock`
- `GetBlock`
- `GetBlockHash`
- `SendRawTransaction`
- `Rescan`
- `NotifyReceived`
- `NotifyBlocks`
- `Notifications`

这让 `wallet` 不直接依赖某一个后端实现。

工程意义上：

- 钱包逻辑依赖抽象
- 后端可以是 btcd RPC、neutrino，甚至其他驱动

### 6.2 链通知怎么进入钱包

`Wallet.SynchronizeRPC()` 会：

1. 保存 `chainClient`
2. 根据后端类型设置 birthday
3. 启动：
   - `handleChainNotifications()`
   - `rescanBatchHandler()`
   - `rescanProgressHandler()`
   - `rescanRPCHandler()`

`handleChainNotifications()` 处理这些事件：

- `chain.ClientConnected`
- `chain.BlockConnected`
- `chain.BlockDisconnected`
- `chain.RelevantTx`
- `chain.FilteredBlockConnected`
- `chain.RescanProgress`
- `chain.RescanFinished`

你可以把它理解成“钱包状态和链状态重新对齐”的总入口。

### 6.3 UTXO 是怎么取出来的

`wallet.UnspentOutputs(policy)` 的流程大致是：

1. 从 `wtxmgr` 取所有未花费输出
2. 用当前同步高度过滤最少确认数
3. 通过 `txscript.ExtractPkScriptAddrs()` 提取地址
4. 用 `waddrmgr.AddrAccount()` 判断属于哪个 account
5. 组装成 `TransactionOutput`

几个阅读时要注意的细节：

- 这是“钱包视角 UTXO”，不是链全局 UTXO
- 它只看钱包已知交易
- 如果脚本无法解析或者没有地址，会被跳过
- account 归属判断依赖脚本可逆推出地址

### 6.4 交易创建为什么必须串行

`CreateSimpleTx()` 最关键的设计是：  
**它不直接选币，而是把请求交给 `txCreator()` 单线程处理。**

原因很现实：

- coin selection 如果并发执行，很容易重复挑选同一个 UTXO
- 一旦并发签名/构造，很容易制造双花式内部竞争

因此代码用 channel 把“创建交易”变成钱包内核里的串行任务。

### 6.5 普通发送和指定输入发送

钱包提供两条常用路径：

- `SendOutputs(...)`
  - 钱包自己选输入
- `SendOutputsWithInput(...)`
  - 调用方显式指定输入 outpoints

OBTC 续期相关代码几乎都走第二条，因为“要续期哪个 UTXO”是显式决定的。

### 6.6 PSBT 路径和直接广播路径的区别

#### 直接广播路径

`legacyrpc/obtc_methods.go` 和 `wallet/autorenew.go` 主要走：

```text
SendOutputsWithInput()
  -> CreateSimpleTx()
  -> reliablyPublishTransaction()
```

特点：

- 一步到位
- 没有 preview/sign/publish 分层
- 更接近传统钱包

#### PSBT-first 路径

`AgentWalletService` 主要走：

```text
PreviewRenewal()
  -> wallet.FundPsbt()
SignPsbt()
  -> signerBackend.FinalizePsbt()
PublishTransaction()
  -> wallet.PublishTransaction()
```

特点：

- 先规划，再签名，再发布
- 便于 capability、审计和外部 signer 接入
- 适合 agent / automation 场景

### 6.7 `FundPsbt()` 为什么重要

`wallet/psbt.go` 的 `FundPsbt()` 不是简单拼接字段，它做了很多钱包侧重活：

- 校验输出不是 dust
- 如果 PSBT 没有输入，就做 coin selection
- 如果调用方已提供输入，就补齐输入 UTXO 信息并测算是否足够
- 自动生成 change
- 调用 `DecorateInputs()`
- 最后做一次 `psbt.InPlaceSort()`，按 BIP69 排序

所以对于 agent 路径来说，`FundPsbt()` 才是真正把“钱包知识”注入到操作里的关键点。

---

## 第七章：OBTC 到期模型在钱包侧是怎么落地的

### 7.1 `wallet/expiry.go` 只做“钱包视图”，不做链状态存储

这个文件很短，但非常关键。它定义了 4 个核心函数：

- `CalculateExpiryHeight`
- `ClassifyExpiryStatus`
- `EstimateDaysToExpiry`
- `BuildExpiryInfo`

这 4 个函数的定位非常清晰：

- 输入：创建高度、当前 tip、高度窗口、dust 阈值等
- 输出：面向钱包 UX / RPC / agent 的统一 expiry 视图

它没有数据库读写，也没有链索引。

### 7.2 钱包侧 expiry 信息长什么样

`ExpiryInfo` 结构体包含：

- `CreateHeight`
- `ExpiryHeight`
- `BlocksToExpiry`
- `DaysToExpiry`
- `Status`
  - `ok`
  - `expiring`
  - `expired`
- `DustRisk`

这意味着钱包当前主要做的是：

1. 给 UTXO 算一个生命周期位置
2. 给调用方一个风险提示

### 7.3 `DustRisk` 的含义

这里最容易误解。

`DustRisk` 不是“当前 UTXO 是 dust”，而是：

**如果这个 UTXO 将来进入 expiry / reclaim 语义，它的预计可回收金额是否会低于 dust 阈值。**

在 `BuildExpiryInfo()` 里，`DustRisk` 由 `projectedReclaimSat < dustThresholdSat` 决定。

### 7.4 `wallet/expiry_policy.go` 是“钱包如何知道 expiry 规则”的入口

这个文件定义了：

- 兼容兜底值
  - `CompatibilityExpiryWindowBlocks = 3679200`
  - `CompatibilityDustThresholdSat = 546`
- 默认 reclaim ratio
  - `DefaultProjectedReclaimRatioBps = 7000`
- `ResolvedExpiryPolicy`
- `ResolveExpiryPolicy(params *chaincfg.Params)`

核心逻辑是：

1. 如果 `params` 能被 `chaincfg.GetExpiryParams()` 识别为 OBTC 网络：
   - 直接读 `obtcd` 的 canonical expiry params
   - `Source = "obtcd_chaincfg"`
2. 否则：
   - 使用 compatibility defaults
   - `Source = "compatibility_default"`
   - 返回 warning

### 7.5 这里有一个必须注意的当前实现边界

从代码可以直接看出：

- `ResolveExpiryPolicy(&chaincfg.ObtcMainNetParams)` 会得到真正的 OBTC 值
- `ResolveExpiryPolicy(&chaincfg.MainNetParams)` 会走 fallback

当前主程序的 `activeNet` 现在已经支持显式切换到：

- `netparams.ObtcMainNetParams`
- `netparams.ObtcTestNetParams`
- `netparams.ObtcRegTestParams`

因此你在阅读时要清楚：

**“钱包已经可以直接调用 OBTC chaincfg API” 与 “当前运行实例已经显式用 `--obtc*` 选到了 OBTC 网络参数” 仍然是两回事。**

默认路径依然是 Bitcoin 网络参数，只有显式传入 `--obtcmainnet`、`--obtctestnet` 或 `--obtcregtest` 时，主程序才会切到原生 OBTC network params。

### 7.6 expiring 阈值怎么来的

如果拿到了真正的 expiry window，默认 expiring 阈值是：

```text
threshold = min(windowBlocks / 4, 144 * 180)
```

也就是：

- 默认用到期窗口的 1/4
- 但上限封顶约 180 天

对应测试里可以看到：

- mainnet `362880 -> 25920`
- testnet `1008 -> 252`
- regtest `144 -> 36`

### 7.7 Legacy / AutoRenew / Agent 三条路径在 expiry 计算上并不完全相同

这是一个很值得新人注意的实现细节：

- `legacyrpc/obtc_methods.go` 的 `getExpiry()`：
  - `projectedReclaimSat = amountSat * 70 / 100`
- `wallet/autorenew.go`：
  - 同样硬编码按 70% 估算 reclaim
- `AgentWalletService`：
  - 用 `policy.ProjectedReclaimRatioBps`
  - 默认也是 7000 bps
  - 但它是显式 policy 字段，不是硬编码常量

所以当前代码在演进方向上已经出现分层：

- 旧路径：把 70% 当内嵌假设
- 新路径：把 reclaim ratio 当 policy 一部分显式建模

这也是为什么 agent 路径更值得优先理解。

---

## 第八章：Legacy RPC 扩展是怎么工作的

### 8.1 注册点在哪里

`rpc/legacyrpc/methods.go` 注册了两个 OBTC 方法：

- `obtc.getexpiry`
- `obtc.renew`

具体实现都在：

- `rpc/legacyrpc/obtc_methods.go`

### 8.2 `obtc.getexpiry` 的处理流程

调用链：

```text
getExpiry()
  -> ResolveExpiryPolicy()
  -> wallet.UnspentOutputs()
  -> makeGetExpiryResult()
  -> wallet.BuildExpiryInfo()
```

几个要点：

- 默认 `limit = 100`
- 支持：
  - `limit`
  - `before_height`
  - `window_blocks`
  - `expiring_threshold`
- 输出按：
  1. `ExpiryHeight`
  2. `OutPoint`
  稳定排序

### 8.3 `obtc.renew` 的处理流程

调用链：

```text
getRenew()
  -> parseOutPointStrings()
  -> parseRenewMinConf()
  -> parseRenewFeeRate()
  -> parseRenewAmount()
  -> 解析或生成 target address
  -> wallet.SendOutputsWithInput()
```

它本质上就是：

- 指定若干输入 outpoints
- 构造一个把金额打到目标地址的新交易
- 广播

这不是链侧 REAP，也不是特殊交易版本，只是钱包侧普通自我转账。

### 8.4 `obtc.renew` 为什么能表达“续期”

因为 OBTC 的 expiry 依赖 UTXO 创建高度。

所以对钱包来说，所谓“续期”就是：

1. 花掉旧 UTXO
2. 产生新的目标输出
3. 新输出的创建高度更晚
4. 于是新的 expiry height 被整体向后推

### 8.5 旧路径的优点和局限

优点：

- 简单直接
- 易于手工调用和验证

局限：

- 没有 capability 授权模型
- 没有 signer session
- 没有 operation / decision log
- 没有 reservation 语义
- 没有 preview/sign/publish 分层

所以它更像“兼容层”，不是长期主路径。

---

## 第九章：自动续期调度器是怎么工作的

### 9.1 配置结构分两层

`wallet/autorenew.go` 把配置拆成两层：

#### `AutoRenewPolicy`

关注“筛谁”：

- `Enabled`
- `WindowStartBlocks`
- `WindowEndBlocks`
- `MaxUtxosPerRun`
- `MaxFeeRateSatPerKB`

#### `AutoRenewRuntimeConfig`

关注“怎么跑”：

- `Policy`
- `Interval`
- `FailureBackoff`
- `Amount`
- `MaxRenewAmountPerRun`
- `MinConf`
- `Account`
- `ExpiryWindowBlocks`
- `ExpiringThresholdBlocks`
- `Label`

### 9.2 配置从哪里来

参数入口在：

- `config.go`
- `sample-btcwallet.conf`

关键配置项包括：

- `autorenew`
- `autorenewinterval`
- `autorenewfailurebackoff`
- `autorenewamount`
- `autorenewmaxrenewamountperrun`
- `autorenewminconf`
- `autorenewwindowstart`
- `autorenewwindowend`
- `autorenewmaxutxos`
- `autorenewmaxfeerate`
- `autorenewexpirywindow`
- `autorenewexpiringthreshold`

### 9.3 自动续期什么时候开始

启动路径是：

```text
btcwallet.go
  -> loader.RunAfterLoad(...)
  -> autoRenewRuntimeConfigFromOptions(cfg)
  -> Wallet.ConfigureAutoRenew()
```

`Wallet.Start()` 时如果发现：

- 已配置 auto-renew
- 且 `Policy.Enabled = true`

就会启动 `autoRenewLoop()`。

### 9.4 `ConfigureAutoRenew()` 做了哪些事

它会：

1. 校验 runtime config
2. 用 `ResolveExpiryPolicy()` 解析 expiry policy
3. 如果调用方没显式覆盖 window / threshold，则注入 resolved policy 的值
4. 保存到 `Wallet` 的运行时字段
5. 如钱包已经启动且需要启用，则拉起 `autoRenewLoop()`

注意：

**它明确注明“不持久化到磁盘”。**

这说明 autorenew 目前仍然是“进程级调度器”，不是钱包数据库里的长期策略对象。

### 9.5 `autoRenewLoop()` 的执行门槛

每一轮运行前会检查：

- 配置是否存在
- `Policy.Enabled`
- `w.ChainSynced()`
- `w.Locked()`
- 失败退避是否仍生效

只有都通过，才会进 `runAutoRenewOnce()`。

### 9.6 候选筛选逻辑

`buildAutoRenewCandidates()` 的核心步骤是：

1. `UnspentOutputs()` 取账户内可用 UTXO
2. 以当前 `tipHeight` 计算每个 UTXO 的 expiry info
3. 只保留位于 `[WindowEndBlocks, WindowStartBlocks]` 窗口内的项
4. 过滤掉 `amountSat <= renewAmount` 的 UTXO
5. 按：
   - `blocksToExpiry` 升序
   - `outpoint.String()` 升序
   排序
6. 如超过 `MaxUtxosPerRun`，截断

因此 auto-renew 的基本策略是：

**优先续期更接近到期的 UTXO。**

### 9.7 预算和失败退避

新增的两个运行时控制是：

#### 每轮预算上限

`limitAutoRenewCandidatesByBudget()` 根据：

- `renewAmount`
- `maxRenewAmountPerRun`

计算本轮最多允许续多少期。

#### 失败退避

如果某轮出现失败且配置了 `FailureBackoff`，则：

- 设置 `autoRenewNextAllowedRun = now + backoff`
- 在此之前的轮次都会跳过

### 9.8 自动续期最后怎么发交易

`renewAutoCandidate()` 的逻辑非常朴素：

1. 为账户生成新地址
2. 生成目标输出脚本
3. 调用 `SendOutputsWithInput()`
4. 显式指定待续期 outpoint

也就是说 auto-renew 仍走传统直接广播路径，不走 agent operation 审计模型。

这点非常重要，因为很多人会误以为“agent wallet 出来以后，autorenew 一定已经切过去”。  
当前并没有。

---

## 第十章：Agent Wallet 是本仓最重要的新架构

### 10.1 为什么它重要

Agent Wallet 并不是“再加一套 RPC”，而是把钱包操作从：

```text
直接调用 -> 立即签名 -> 立即广播
```

改造成：

```text
观察 -> 规划 -> 授权 -> 开会话 -> 签名 -> 广播 -> 审计
```

这对 OBTC 特别重要，因为 OBTC 钱包不是单纯转账钱包，而是 expiry risk 管理系统。

### 10.2 协议入口：`rpc/agentwallet/agentwallet.proto`

`AgentWalletService` 当前提供的方法可以按功能分组：

#### 状态观察

- `GetWalletState`
- `ListUtxos`
- `GetExpiryRisk`

#### 授权与会话

- `IssueCapability`
- `RevokeCapability`
- `OpenSignerSession`
- `GetSignerSession`
- `CloseSignerSession`

#### 续期执行

- `PreviewRenewal`
- `SignPsbt`
- `PublishTransaction`
- `SubmitRenewal`

#### 资源预留与审计

- `ReserveUtxos`
- `ReleaseReservation`
- `GetOperation`
- `ListOperations`

### 10.3 四个核心概念

#### 1. Capability

它是权限令牌，核心字段包括：

- `capability_id`
- `wallet_id`
- `principal`
- `permissions`
- `expires_at`
- `revoked`

理解方式：

**能力不是“全局钱包密码”，而是带过期时间和权限范围的授权对象。**

#### 2. Signer Session

它是签名会话，核心字段包括：

- `signer_session_id`
- `capability_id`
- `principal`
- `permissions`
- `expires_at`
- `closed`

理解方式：

**capability 是授权，signer session 是“在某段时间里实际持有签名能力”的运行时会话。**

#### 3. Reservation

它是 UTXO 预留，底层通过 `wallet.LeaseOutput()` 实现。

理解方式：

**reservation 让 planning 和 execute 之间不会因为别的请求抢走同一个 UTXO。**

#### 4. Operation

它是整个续期动作的持久化记录，里面不仅有摘要，还带：

- `history`
- `decision_log`
- `latest_policy_snapshot`
- `latest_signer_proof`

理解方式：

**operation 不是一次 RPC 响应，而是一个完整的审计对象。**

### 10.4 `GetWalletState()` 的意义

它返回：

- 网络
- 是否已加载
- 是否链同步
- 是否锁定
- 当前块高和块哈希
- 当前 signer backend 信息

对 CLI / agent 来说，这比 legacy RPC 好得多，因为它能明确告诉你：

- 现在是不是应该继续续期
- 目前是不是 local signer / remote signer / publish_only

### 10.5 `GetExpiryRisk()` 是 agent 视角下的 `obtc.getexpiry`

处理流程是：

```text
lookupOutputs()
  -> resolveExpiryPolicy()
  -> buildExpiryRiskItems()
  -> BuildExpiryInfo()
```

和 legacy 的差别：

- 支持按 outpoints 精确查询
- 返回 `effective_policy`
- `projected_reclaim_ratio_bps` 是 policy 一部分
- 可以把 warning 带回客户端

### 10.6 `PreviewRenewal()` 是真正的“规划阶段”

这是整个新架构里最值得精读的函数之一。

它会做：

1. 校验 account / min conf / target amount
2. 通过 `lookupOutputs()` 找到指定 UTXO
3. 如给了 reservation_id，校验这些 UTXO 的租约归属
4. 解析或生成目标地址
5. 构造一个只包含目标输出的 PSBT skeleton
6. 调用 `wallet.FundPsbt()` 让钱包补齐输入/找零/费用
7. 生成 `TransactionSummary`
8. 解析 expiry policy 并重算选中 UTXO 的 expiry risks
9. 创建 `Operation`
10. 把 unsigned PSBT 和 operation 一起持久化

这里有两个很容易忽略的细节：

#### 细节 1：`PreviewRenewal` 永远是 dry-run

即使请求里 `DryRun = false`，它也会返回 warning：

```text
PreviewRenewal is always dry-run and does not publish transactions
```

也就是说 preview 的职责是“规划并持久化草稿”，不是执行。

#### 细节 2：preview 会持久化

它不是临时计算结果，而是会写入：

- operation
- unsigned PSBT artifacts

后面的 sign/publish 都基于这份草稿继续推进。

### 10.7 `SignPsbt()` 做了什么

`SignPsbt()` 最终会走到 `signRenewalOperation()`：

1. 读取 operation 和 artifacts
2. 校验 signer session / capability 权限
3. 如果还没有 unsigned PSBT，就重建
4. 调用 `signerBackend.FinalizePsbt()`
5. 提取 signed PSBT 和 raw tx
6. 更新 operation 状态为 `SIGNED`
7. 生成：
   - `OperationEvent`
   - `DecisionLogEntry`
   - `SignerProof`
8. 持久化

这里你要特别注意：

- 签名动作不是直接调用钱包某个裸函数
- 它一定要经过 capability / signer session / policy snapshot / signer proof 这些审计层

### 10.8 `PublishTransaction()` 与 `SubmitRenewal()` 的区别

#### `PublishTransaction()`

作用：

- 发布一个已经签好的 PSBT
- 可以使用：
  - 存储里的 signed PSBT
  - 或者请求里传入的 `signed_psbt` override

如果传入 override，它还会生成 `external_signed_psbt` 类型的 signer proof。

#### `SubmitRenewal()`

作用：

- 兼容性封装
- 如果 operation 仍是 `DRAFT`：
  - 先走 `SignPsbt`
- 然后再走 `PublishTransaction`

所以：

- `SignPsbt + PublishTransaction` 是分层 API
- `SubmitRenewal` 是一键执行 API

### 10.9 Reservation 语义怎么落地

`ReserveUtxos()` 会：

1. 找到目标输出
2. 给每个 outpoint 调 `wallet.LeaseOutput()`
3. 如果中途失败，回滚已租约输出
4. 持久化 reservation record

`PreviewRenewal()` 在有 `reservation_id` 时会检查：

- 这些 outpoint 是否真的被该 reservation 持有

`PublishTransaction()` 在成功广播后会：

- 尝试释放租约
- 并更新 reservation 元数据为 released

这整套设计是为了把：

**plan 阶段的资源占用**

和

**execute 阶段的真实广播**

安全衔接起来。

### 10.10 Operation 的审计结构很值得细看

`Operation` 里最重要的几类字段：

#### 业务摘要

- `kind`
- `state`
- `outpoints`
- `summary`
- `txid`

#### 风险快照

- `policy_verdict`
- `warnings`
- `expiry_risks`
- `effective_policy`
- `latest_policy_snapshot`

#### 审计轨迹

- `history`
- `decision_log`
- `latest_signer_proof`

如果你在做后续 AI / automation / compliance 设计，这一层是重点，不是附属品。

### 10.11 持久化是怎么做的

`rpc/rpcserver/agentwallet_persistence.go` 在钱包 DB 里创建了一个 `agentwallet` 根 bucket，并细分为：

- `operations`
- `artifacts`
- `reservations`
- `capabilities`
- `signer_sessions`

一个很值得注意的实现细节：

- `Operation` 直接用 protobuf 存
- 其他记录多用 JSON 存

这说明作者对 `Operation` 的看法是“协议对象本身就是一等持久化对象”。

### 10.12 Service restart 后怎么处理 signer session

`ensurePersistenceLoaded()` 在加载持久化数据后，会调用：

- `reconcileRecoveredSignerSessions()`

它的策略不是“恢复旧会话继续用”，而是：

- 把所有未关闭的 signer session 标记为因 `service_restart` 关闭

这是一种非常保守但合理的安全选择。

### 10.13 权限校验怎么做

`agentwallet_auth.go` 做了三件关键事：

1. capability 校验：
   - 是否存在
   - 是否 revoked / expired
   - wallet_id 是否匹配
   - principal 是否匹配
   - permission 是否足够
2. signer session 校验：
   - 是否绑定在该 capability 下
   - 是否未关闭/未过期
   - signer backend 是否认可该 session
3. capability 撤销联动：
   - `RevokeCapability()` 会尝试关闭关联 signer sessions

这就是为什么 agent 路径的安全边界明显强于 legacy RPC。

---

## 第十一章：`renewall` CLI 已经切到 agent gRPC 主路径

### 11.1 这个 CLI 现在不是 legacy RPC 包装器

`cmd/renewall/main.go` 当前主路径是：

1. 连接 `AgentWalletService`
2. `GetWalletState`
3. `GetExpiryRisk`
4. 根据窗口筛选 outpoints
5. 如非 dry-run：
   - `IssueCapability`
   - `OpenSignerSession`
   - 对每个 outpoint：
     - `PreviewRenewal`
     - `SubmitRenewal`
6. 最后：
   - `CloseSignerSession`
   - `RevokeCapability`

所以它已经不再以 `obtc.renew` 为主路径。

### 11.2 过滤逻辑在哪里

CLI 自己维护了一个 `renewFilter`：

- `includeExpired`
- `windowStart`
- `windowEnd`

然后基于 `GetExpiryRisk` 的结果做筛选。

这说明：

- risk computation 在服务端
- batch policy 在 CLI 层

### 11.3 `renewall` 支持什么模式

#### dry-run

只打印选中的 outpoints，不开签名会话。

#### 单次执行

跑一轮后退出。

#### 定时多轮执行

通过：

- `--interval`
- `--runs`

实现。

### 11.4 `walletpass` 在不同 signer backend 下语义不同

CLI 明确写了：

- local signer mode：`walletpass` 是钱包私钥口令
- remote signer mode：`walletpass` 是 remote signer auth bytes

因此你在调试 CLI 时，必须先看 `GetWalletState().signer_backend.mode`。

### 11.5 目前 CLI 还不支持 publish-only 外部签名路径

`requireExecutionBackend()` 会拒绝：

- `publish_only`

并给出错误：

```text
publish_only signer backend requires external signed PSBT; renewall CLI does not support that path yet
```

这说明当前 CLI 仍主要服务于：

- local signer
- remote signer transport

而不是纯 watch-only + 外部手工签名。

---

## 第十二章：配置、网络参数与当前实现边界

### 12.1 `config.go` 做的事情比你想象得多

除了常规配置解析，它还负责：

- 切网络
- 规范化路径
- 初始化日志
- 处理 wallet create / create temp
- 设置默认 RPC listeners
- 校验 client/server TLS 约束
- 校验 remote signer 地址和证书
- 校验 auto-renew 参数

所以很多“启动前就失败”的问题都在这里。

### 12.2 当前支持的主程序网络选择

从 `config.go` 可以直接看到当前支持：

- mainnet
- obtcmainnet
- obtctestnet
- obtcregtest
- testnet3
- testnet4
- simnet
- signet
- regtest

这是在 inherited `btcwallet` 选网骨架上补出来的 OBTC 原生网络选择。

### 12.3 这里与 OBTC network params 之间的关系要特别小心

虽然当前代码已经：

- 在编译期链接 `../obtcd`
- 在 wallet 层调用 `chaincfg.GetExpiryParams()`

现在主程序也已经能显式切换到：

- `chaincfg.ObtcMainNetParams`
- `chaincfg.ObtcTestNetParams`
- `chaincfg.ObtcRegTestParams`

因此从纯代码事实出发，当前状态更准确地说是：

- **钱包已经具备直接读取 OBTC expiry params 的能力**
- **主程序在显式传入 `--obtc*` flags 时，也会切到原生 OBTC 网络参数选择**

剩余要注意的是默认网络仍然不是 OBTC；如果运行实例没有显式带上 `--obtcmainnet`、`--obtctestnet` 或 `--obtcregtest`，就不会自动落到 OBTC 参数分支。

### 12.4 auto-renew 配置在哪看

最方便的入口是：

- `sample-btcwallet.conf`

里面已经把 auto-renew 选项全部列出来了。

如果你要读代码，配置映射关系看：

- `config` struct
- `autoRenewRuntimeConfigFromOptions()`
- `wallet.ValidateAutoRenewRuntimeConfig()`

### 12.5 remote signer 配置在哪看

同样在：

- `sample-btcwallet.conf`
- `config.go`
- `rpcserver.go`
- `rpc/rpcserver/agentwallet_remote_signer.go`

入口参数包括：

- `agentremotesigneraddr`
- `agentremotesignercafile`
- `agentremotesignernotls`
- `agentremotesignerservername`
- `agentremotesignerauthtoken`
- `agentremotesignerdialtimeout`

### 12.6 signer backend 的选择规则

`startWalletRPCServices()` 会调用 `buildAgentWalletOptions()`。

最终 backend 选择逻辑是：

1. 如果显式配置了 remote signer 地址：
   - 使用 `remote` backend
2. 否则如果钱包是 watch-only：
   - 使用 `publish_only` backend
3. 否则：
   - 使用 `local` backend

这条规则决定了后续所有 agent 行为。

### 12.7 local / remote / publish_only 三种 backend 的本质区别

#### `local`

- 依赖 `wallet.Unlock()`
- 最多 1 个 active session
- `FinalizePsbt()` 直接调用钱包本地签名

#### `remote`

- 不在本进程持有私钥
- 可以开很多 session
- 可通过 gRPC transport 把 PSBT 发给远程 signer
- 可以带回 attestation / receipt / device metadata

#### `publish_only`

- 不能本地开 signer session
- 只能接受外部已经签好的 PSBT
- 适合 watch-only 或外部签名部署

### 12.8 一个很重要的实现观察

当前主程序虽然已经能挂 remote signer backend，但：

- auto-renew 仍直接走钱包发送路径
- `renewall` CLI 还不支持 publish-only
- legacy `obtc.renew` 也不走 agent capability 模型

所以这个仓库目前是“新老两套执行模型并存”，而不是已经完全统一。

---

## 第十三章：测试与验证材料怎么看

### 13.1 和 OBTC 新功能最相关的测试文件

- `wallet/expiry_test.go`
- `wallet/expiry_policy_test.go`
- `wallet/autorenew_test.go`
- `config_autorenew_test.go`
- `rpc/legacyrpc/obtc_methods_test.go`
- `rpc/rpcserver/agentwallet_server_test.go`
- `rpc/rpcserver/agentwallet_signer_test.go`
- `rpc/rpcserver/agentwallet_remote_signer_test.go`
- `rpc/rpcserver/agentwallet_persistence_test.go`
- `cmd/renewall/main_test.go`

如果你只想先理解 OBTC 新增逻辑，这些测试足够支撑第一轮阅读。

### 13.2 文档化验证证据

这两个文档很适合和代码对照着看：

- `docs/phase5_implementation_summary.md`
- `docs/phase5-validation.md`

它们分别回答：

- 现在实现到了哪
- 实际验证跑过什么

### 13.3 关于全量测试的现实情况

链相关测试有一部分依赖 `bitcoind` 或更完整的链环境。  
所以本仓的实际工程习惯是：

- 新增功能优先补目标模块单测
- 涉及 `chain/` 的全链路测试视环境而定

如果你看到某些工程脚本或提交里在特意规避 `chain/` 的 pre-push 全量要求，不要意外，这正是这类依赖导致的。

### 13.4 测试阅读建议

#### 理解 expiry 规则

先看：

- `wallet/expiry_test.go`
- `wallet/expiry_policy_test.go`

#### 理解 manual renew

再看：

- `rpc/legacyrpc/obtc_methods_test.go`

#### 理解 auto-renew 策略

再看：

- `wallet/autorenew_test.go`
- `config_autorenew_test.go`

#### 理解 agent architecture

最后看：

- `rpc/rpcserver/agentwallet_server_test.go`
- `rpc/rpcserver/agentwallet_signer_test.go`
- `rpc/rpcserver/agentwallet_remote_signer_test.go`

---

## 第十四章：推荐阅读路径

### 路径 A：两小时快速建立全局认知

1. `btcwallet.go`
2. `config.go`
3. `wallet/loader.go`
4. `wallet/wallet.go`
5. `wallet/utxos.go`
6. `wallet/psbt.go`
7. `wallet/expiry.go`
8. `rpc/legacyrpc/obtc_methods.go`
9. `wallet/autorenew.go`
10. `rpc/agentwallet/agentwallet.proto`
11. `rpc/rpcserver/agentwallet_server.go`
12. `cmd/renewall/main.go`

### 路径 B：只想读 OBTC 续期功能

1. `docs/phase5_implementation_summary.md`
2. `wallet/expiry.go`
3. `wallet/expiry_policy.go`
4. `rpc/legacyrpc/obtc_methods.go`
5. `wallet/autorenew.go`
6. `cmd/renewall/main.go`
7. `docs/phase5-validation.md`

### 路径 C：只想读 agent wallet 和远程签名

1. `rpc/agentwallet/agentwallet.proto`
2. `rpc/rpcserver/agentwallet_server.go`
3. `rpc/rpcserver/agentwallet_auth.go`
4. `rpc/rpcserver/agentwallet_signer.go`
5. `rpc/rpcserver/agentwallet_remote_signer.go`
6. `rpc/rpcserver/agentwallet_persistence.go`
7. `cmd/renewall/main.go`

### 路径 D：想把底层钱包骨架补扎实

1. `wallet/loader.go`
2. `wallet/wallet.go`
3. `wallet/chainntfns.go`
4. `wallet/rescan.go`
5. `wallet/utxos.go`
6. `wallet/createtx.go`
7. `wallet/psbt.go`
8. `waddrmgr/manager.go`
9. `wtxmgr/tx.go`
10. `walletdb/interface.go`

### 路径 E：想读设计文档，但要知道哪些地方是“设计草案”

推荐顺序：

1. 先读本指南
2. 再读 `docs/phase5_implementation_summary.md`
3. 最后读 `docs/developer/ai_agent_wallet_interface_zh.md`

原因是：

- `ai_agent_wallet_interface_zh.md` 很有价值
- 但它兼有“设计前瞻”和“历史阶段记录”双重性质
- 某些早期背景说明已经不完全等于当前实现

所以它更适合作为“方向性文档”，不是逐行映射当前代码的实现文档。

---

## 附录 A：关键文件速查表

| 文件 | 建议优先级 | 作用 |
|------|------------|------|
| `btcwallet.go` | 极高 | 主启动链路 |
| `config.go` | 极高 | 配置、网络、TLS、auto-renew、remote signer |
| `rpcserver.go` | 极高 | RPC 服务装配、agent backend 注入 |
| `wallet/loader.go` | 极高 | 打开/创建钱包和回调装配 |
| `wallet/wallet.go` | 极高 | 钱包内核、goroutine、交易发送、锁状态 |
| `wallet/utxos.go` | 极高 | 钱包 UTXO 读取 |
| `wallet/psbt.go` | 极高 | PSBT 资金编排和签名终结点 |
| `wallet/chainntfns.go` | 高 | 链通知进钱包的主入口 |
| `wallet/expiry.go` | 极高 | 钱包侧 expiry 视图核心 |
| `wallet/expiry_policy.go` | 极高 | expiry 规则来源与 fallback |
| `wallet/autorenew.go` | 极高 | 自动续期调度器 |
| `rpc/legacyrpc/obtc_methods.go` | 极高 | `obtc.getexpiry` / `obtc.renew` |
| `rpc/agentwallet/agentwallet.proto` | 极高 | agent 协议总览 |
| `rpc/rpcserver/agentwallet_server.go` | 极高 | agent 服务主实现 |
| `rpc/rpcserver/agentwallet_auth.go` | 高 | capability / signer session 权限检查 |
| `rpc/rpcserver/agentwallet_signer.go` | 高 | local/remote/publish_only backend |
| `rpc/rpcserver/agentwallet_remote_signer.go` | 高 | remote signer gRPC transport |
| `rpc/rpcserver/agentwallet_persistence.go` | 高 | operation/capability/session 持久化 |
| `cmd/renewall/main.go` | 极高 | CLI 如何走 agent API |
| `sample-btcwallet.conf` | 高 | 所有运行参数的最直观说明 |

---

## 附录 B：新人最容易搞错的点

### 1. “续期”不是 REAP

- REAP 是链侧系统交易，由 `obtcd` 共识和挖矿模板处理
- renew 是钱包侧普通交易，用来刷新 UTXO 创建高度

### 2. 钱包没有自己的 ExpiryIndex

- 钱包侧只是用 `create_height + window_blocks` 计算风险视图
- 真正的链级 expiry 状态在 `obtcd`

### 3. `AutoRenew` 不是持久化 policy 系统

- 当前只是进程内 runtime config
- 重启后需要重新注入配置

### 4. agent wallet 不是独立钱包实例

- 它和 legacy RPC、WalletService 共享同一个 `*wallet.Wallet`
- 差别在接口语义和执行/审计模型

### 5. `renewall` 现在主要走 agent gRPC，不是 legacy RPC

- 不要再把它当成 `obtc.renew` 的简单脚本包装

### 6. local signer backend 本质上还是全局 unlock 的会话化包装

- 它比直接 unlock 更安全
- 但底层仍然依赖钱包解锁
- 所以它只能支持一个 active session

### 7. remote signer backend 不等于 publish-only

- remote signer 可以真正代理 `FinalizePsbt`
- publish-only 则完全不做签名，只接受外部成品

### 8. 当前 expiry policy 直连 `obtcd` 并不代表主程序网络选择已完全原生 OBTC 化

- 代码里这两件事仍然是分开的
- 读代码时要把“能力”和“主路径装配”区分开

---

## 附录 C：术语表

- **UTXO**：未花费交易输出
- **Expiry**：UTXO 的到期高度语义
- **Expiring**：接近到期但尚未到期
- **Renew**：通过花费旧 UTXO 生成新 UTXO 来刷新其生命周期
- **REAP**：链侧到期资产回收协议，由 `obtcd` 实现
- **Legacy RPC**：兼容 Bitcoin Core 风格的 JSON-RPC
- **WalletService**：通用实验性 gRPC 钱包接口
- **AgentWalletService**：面向 agent/automation 的新型执行接口
- **Capability**：带权限、principal、TTL 的授权对象
- **Signer Session**：一次短生命周期签名会话
- **Reservation**：基于 output lease 的 UTXO 预留
- **Operation**：带审计历史的持久化动作对象
- **Signer Proof**：对签名动作、签名来源和证据的记录
- **Publish-only backend**：不负责签名，只允许发布外部已签交易的 backend
