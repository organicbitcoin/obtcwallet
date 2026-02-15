# AGENTS.md


## 结构总览（obtcwallet 当前建议）

```
obtcwallet/ (repo root)
  btcwallet.go          # 主程序入口
  config.go             # 配置与参数解析
  rpcserver.go          # 钱包 RPC 服务入口

  wallet/               # 核心钱包逻辑（Phase 5 主要落位）
    (planned)
    expiry.go           # 到期状态计算/筛选
    renew.go            # 续期交易构造与提交
    policy.go           # 自动续期策略（可选）

  waddrmgr/             # 地址管理（密钥/账户/地址）
  wtxmgr/               # 交易历史与 UTXO 管理
  walletdb/             # 钱包数据库抽象

  rpc/                  # RPC 相关结构与子模块
    (planned)
    obtc/               # obtc.getexpiry / obtc.renew

  cmd/                  # 命令行程序
    dropwtxmgr/
    sweepaccount/
    (planned)
    renew-all/          # 批量续期入口

  chain/                # 链后端交互（与全节点通信）
  netparams/            # 网络参数适配
  internal/             # 内部通用模块

  docs/
    phase5_execution_plan.md
    phase5_execution_plan_zh.md
    phase5-validation.md      # Phase 5 验证记录（待补）

  scripts/              # 辅助脚本
  build/                # 构建与发布辅助资源
```

## 说明

- 上述结构为“建议落位”，已实现与规划项混合在一起，便于对照阶段计划（phase）。
- 若实际目录不同，以仓库现状为准，可在此文件同步更新。
- 新增模块尽量按功能归类，避免在顶层堆积零散文件。

## Git 工作指导

- 默认流程：`新分支 -> 开发 -> 本地测试 -> commit -> push -> PR -> merge`。
- 非紧急情况不要直接推 `master`；优先通过 PR 合入。
- 分支命名建议：
  - `feat/...` 功能
  - `fix/...` 修复
  - `test/...` 测试增强
  - `docs/...` 文档更新
  - `chore/...` 工程与维护
- commit message 使用英文，建议前缀：`feat:` / `fix:` / `test:` / `docs:` / `chore:`。
- 小步提交：每个 commit 尽量聚焦单一主题（功能、测试、文档分开）。

### 提交前检查（与 obtcd 对齐）

- 本仓启用 `.githooks/`：
  - `pre-commit`：检查 `gofmt` + `go test ./...`
  - `pre-push`：运行 `go test ./...`（若存在 `integration/` 则额外执行）
- 若本机缺少 `bitcoind` 导致 `chain` 测试失败：
  - 优先在可用环境补跑完整测试；
  - 必要时可临时 `--no-verify` 推送，但需在 PR 描述中说明原因与补测计划。

## 交互约束

- 对话回复一律中文。
- 提交记录（commit message）使用英文。
