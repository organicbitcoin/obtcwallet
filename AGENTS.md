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

## 交互约束

- 对话回复一律中文。
- 提交记录（commit message）使用英文。
