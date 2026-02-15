# OBTC Wallet Phase 5 执行计划 v2（可执行版）

> 目标：把 `obtcwallet` 的到期感知与续期能力做成可落地、可测试、可上线迭代的最小闭环。  
> 本版修正点：对齐 `btcwallet` 现有代码结构，明确 5A/5B 边界，避免跨仓职责混淆。

---

## 0. 关键结论（先看）

1. **Phase 5A 必做**：
   - `obtc.getexpiry`（查询）
   - `obtc.renew`（手动续期）
   - 验证文档与测试

2. **本轮不做**：
   - 自动续期（窗口策略/预算/限费）
   - `renew-all` CLI（若后续需要再单独评估）

3. **跨仓边界**：
   - `obtcd`：链规则、到期索引、REAP 共识与模板
   - `obtcwallet`：钱包视角的查询、续期交易构造与提交

---

## 1. 与现有仓库结构对齐

`obtcwallet` 当前已有：
- `rpc/legacyrpc`（JSON-RPC）
- `rpc/rpcserver`（gRPC）
- `wallet/`、`wtxmgr/`、`waddrmgr/`

因此本期建议：

### 5A（优先）先落在 legacyrpc
- 新增 wallet 级方法（`wallet/expiry.go`、`wallet/renew.go`）
- 在 `rpc/legacyrpc` 暴露 `obtc.getexpiry` / `obtc.renew`
- gRPC 可先不做（避免双栈并行放大范围）

### 5B 再补 gRPC/CLI
- 复用 5A wallet 方法，不重复造逻辑

---

## 2. 数据契约（v1）

### 2.1 `obtc.getexpiry` 返回项

每个 UTXO 至少包含：
- `outpoint`：`txid:vout`
- `amount_sat`
- `create_height`
- `expiry_height`
- `blocks_to_expiry`
- `days_to_expiry`（展示字段）
- `status`：`ok | expiring | expired`
- `dust_risk`：布尔值（提示用途）

### 2.2 `obtc.renew` 返回项

- `txid`
- `input_count`
- `output_count`
- `fee_sat`
- `renewed_total_sat`
- `target_address`

---

## 3. 到期参数单一真源（必须）

钱包侧所有到期计算必须读取与 `obtcd` 一致的 OBTC 参数（窗口、启用高度）。

要求：
- 不允许在钱包代码里硬编码独立窗口常量；
- 统一 helper 入口，例如：`wallet/expiry.go` 中 `CalcExpiry(...)`；
- 写明参数来源与兼容策略（主网/测试网/regtest）。

---

## 4. 可执行任务拆解（按提交顺序）

## Task A — wallet 到期计算基础层

**文件建议**：
- `wallet/expiry.go`（新）
- `wallet/expiry_test.go`（新）

**实现内容**：
- 定义内部结构：`ExpiryInfo`
- 提供函数：
  - `CalcExpiryHeight(createHeight, params)`
  - `ClassifyExpiryStatus(tipHeight, expiryHeight)`
  - `EstimateDaysToExpiry(blocksToExpiry)`

**验收**：
- 覆盖边界：`tip = expiry-1 / expiry / expiry+1`
- 单测通过

---

## Task B — `obtc.getexpiry`（legacyrpc）

**文件建议**：
- `rpc/legacyrpc/methods.go`（或同层命令路由文件）
- `rpc/legacyrpc/obtc_methods.go`（新，推荐）
- `rpc/legacyrpc/obtc_methods_test.go`

**实现内容**：
- 方法名：`obtc.getexpiry`
- 支持过滤参数（v1）：
  - `before_height`（可选）
  - `limit`（可选）
- 返回按 `expiry_height asc, outpoint asc` 稳定排序

**验收**：
- 正常场景 + 空结果场景
- 排序稳定性测试

---

## Task C — `obtc.renew`（legacyrpc）

**文件建议**：
- `wallet/renew.go`（新）
- `wallet/renew_test.go`（新）
- `rpc/legacyrpc/obtc_methods.go`

**实现内容**：
- 输入：显式 outpoints（第一版先不做复杂筛选）
- 续期默认策略：
  - 目标地址默认新地址（fresh addr）
  - 校验 `max_feerate`（可选参数）
- 拒绝条件：
  - outpoint 不存在
  - 已过期且不允许续期（按当前策略）
  - 参数非法

**验收**：
- 至少 1 条成功续期集成测试（返回 txid）
- 失败路径错误码/错误文案稳定

---

## Task D — 验证文档（本期必须）

**文件**：`docs/phase5-validation.md`（新）

**内容必须包含**：
- `getexpiry` 请求/响应样例
- `renew` 请求/响应样例
- 至少 2 个失败案例与预期错误
- 一次真实 txid 记录

---

## 5. 5B（下一阶段）

### 5B-1 自动续期
- 触发窗口（到期前区间）
- 每次上限与预算
- 审计日志

### 5B-2 CLI `renew-all`
- **注意**：若 CLI 依赖 `btcctl`，需要在对应仓实现；
- 若坚持在本仓做，可提供本地工具命令，但要避免与既有生态冲突。

---

## 6. 测试策略（最小可交付）

### 单元测试
- 到期计算边界
- 状态分类
- 参数异常
- 排序稳定性

### 集成测试
- 钱包 UTXO -> `getexpiry`
- `renew` 成功后产生新输出
- 失败路径（无效 outpoint/参数非法/费率超限）

### 回归测试
- 重复调用 `getexpiry` 返回顺序一致
- 同请求重复执行时结果可解释（幂等性语义明确）

---

## 7. 风险与防呆

1. **跨仓参数漂移**
- 防呆：参数读取统一入口 + 文档固定参数来源。

2. **范围失控（一次做太多）**
- 防呆：本期只做 5A，自动续期/CLI 批量放 5B。

3. **双 RPC 栈并行导致工期翻倍**
- 防呆：先 legacyrpc，gRPC 后补。

4. **续期费用不可控**
- 防呆：`max_feerate` + 明确失败返回。

---

## 8. 里程碑与 DoD

### M1（基础层）
- `wallet/expiry.go` + 单测

### M2（查询可用）
- `obtc.getexpiry` 可用，排序稳定，测试通过

### M3（续期可用）
- `obtc.renew` 可广播成功，失败路径清晰

### M4（文档闭环）
- `phase5-validation.md` 完整记录

**Phase 5A DoD**：
- [ ] `obtc.getexpiry` 已上线（legacyrpc）
- [ ] `obtc.renew` 已上线（legacyrpc）
- [ ] `go test ./...` 通过
- [ ] `docs/phase5-validation.md` 完成

---

## 9. 下一步立即执行建议

按下面顺序开工最稳：
1. 先做 `wallet/expiry.go` + test；
2. 接 `obtc.getexpiry`；
3. 再做 `wallet/renew.go` + `obtc.renew`；
4. 最后补验证文档。

这样 2–3 次小 PR 就能落地 5A，而不是一次大改难回滚。
