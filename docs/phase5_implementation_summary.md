# OBTC Wallet 实现总结（Phase 5，面向新读者）

> 本文档是 `obtcwallet` 当前实现状态的独立总结，目标是让新读者快速理解：
> 1) 这个仓库在 OBTC 体系中的职责；
> 2) Phase 5 已完成了什么；
> 3) 接下来应该从哪里继续开发与验证。

---

## 1. 一句话定位

`obtcwallet` 负责 **钱包侧的到期感知与续期操作**。  
链侧规则（到期索引、REAP 共识、模板注入）由 `obtcd` 负责。

---

## 2. Phase 5 当前目标（手动续期版本）

本轮范围聚焦 Phase 5A：
- `obtc.getexpiry`：查询钱包 UTXO 的到期状态；
- `obtc.renew`：手动指定 UTXO 续期；
- 相关单元测试与验证文档。

不在本轮范围：
- 自动续期策略（定时/预算/随机窗口）；
- 大规模批量 CLI 工作流（可在后续阶段扩展）。

---

## 3. 已实现内容

### A. 钱包到期基础模型（wallet 层）
文件：`wallet/expiry.go`

已实现函数：
- `CalculateExpiryHeight`
- `ClassifyExpiryStatus`
- `EstimateDaysToExpiry`
- `BuildExpiryInfo`

作用：
- 将 UTXO 的创建高度映射到到期高度；
- 给出 `ok/expiring/expired` 状态；
- 输出天数估算与 `dust_risk` 提示字段。

测试：`wallet/expiry_test.go`

### B. Legacy RPC 扩展（rpc/legacyrpc）
文件：
- `rpc/legacyrpc/obtc_methods.go`
- `rpc/legacyrpc/methods.go`（handler 注册）

已接入命令：
- `obtc.getexpiry`
- `obtc.renew`

`obtc.getexpiry`：
- 支持 limit / before_height 等参数；
- 返回按到期高度与 outpoint 稳定排序。

`obtc.renew`：
- 支持 outpoints + amount + target_address + max_feerate + minconf；
- 默认可生成新地址作为续期目标；
- 返回 txid、输入/输出数量、费率等摘要。

测试：`rpc/legacyrpc/obtc_methods_test.go`

---

## 4. 关键代码索引

- `wallet/expiry.go`：到期计算与状态分类核心
- `wallet/expiry_test.go`：到期核心函数直接单测
- `rpc/legacyrpc/obtc_methods.go`：`obtc.getexpiry` / `obtc.renew` 逻辑
- `rpc/legacyrpc/obtc_methods_test.go`：RPC 辅助函数与参数校验直接单测
- `rpc/legacyrpc/methods.go`：命令路由注册点

---

## 5. 完成度总览

| 模块 | 状态 | 说明 |
|---|---|---|
| 钱包到期计算基础 | ✅ 已完成 | 计算、分类、聚合信息已可用 |
| `obtc.getexpiry` | ✅ 已完成 | legacyrpc 已接入 |
| `obtc.renew`（手动） | ✅ 已完成（v1） | 参数校验 + 指定输入续期路径已接入 |
| 自动续期 | 🟡 基础策略已启动 | 已新增 policy 基础模型与窗口/限额选择函数，尚未接入调度执行链路 |
| 批量 CLI 工作流 | ⏳ 未开始 | 计划后续阶段实现 |
| 验证文档（phase5-validation） | ⚠️ 待补 | 需补完整命令与 tx 证据 |

---

## 6. 测试与质量约束

- 约束：新添加函数必须有直接单元测试。
- 已执行：
  - `wallet/expiry_*` 直接测试；
  - `obtc_methods` 新增 helper/参数解析函数直接测试；
  - 关键路径测试通过。

说明：
- 仓库全量测试在无 `bitcoind` 可执行环境下，`chain` 相关测试可能失败；
- 钱包功能开发阶段优先保证目标模块测试与静态检查稳定通过。

---

## 7. 下一步建议（Phase 5A 收尾）

1. 新增 `docs/phase5-validation.md`：
   - `getexpiry` 请求/响应示例；
   - `renew` 请求/响应示例；
   - 至少 2 个失败用例及预期错误。

2. 增加 `obtc.renew` 成功路径的更完整集成验证：
   - 从可用 UTXO 到广播结果；
   - 校验输入选择与输出目标。

3. 评估并设计 Phase 5B：
   - 自动续期策略开关；
   - 批量续期调度与预算限制。

---

## 8. 术语对照

- **UTXO (Unspent Transaction Output)**：未花费交易输出
- **REAP (Reclaim Expired Assets Protocol)**：到期资产回收机制（链侧）
- **RPC (Remote Procedure Call)**：远程过程调用接口
- **Legacy RPC**：`btcwallet` 兼容层 JSON-RPC 服务
- **Dust**：小于经济可花费阈值的输出
