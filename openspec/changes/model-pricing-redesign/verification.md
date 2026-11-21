# 模型价格重构 + 模型广场 验收记录

> 部署环境：`http://38.39.30.186:8081`（测试服务器）
> 验收时间：2026-11-15
> 结论：**全部通过**

## 一、构建与测试

| 项目 | 命令 | 结果 |
|---|---|---|
| 后端构建 | `go -C backend build ./...` | exit 0 |
| 后端定价/广场测试 | `go -C backend test -tags=unit ./internal/service -run 'Plaza|Catalog|Peak|ModelPricing'` | ok |
| 后端广场 handler | `go -C backend test -tags=unit ./internal/handler -run ModelPlaza` | 11 PASS |
| 后端 API 契约 | `go -C backend test -tags=unit ./internal/server -run TestAPIContracts` | PASS |
| 前端类型检查 | `npx pnpm@10.24.0 typecheck` | exit 0 |
| 前端 i18n | `npx pnpm@10.24.0 check:i18n` | 3 passed |
| 前端全量测试 | `npx pnpm@10.24.0 test:run` | **388 files / 3196 tests passed, 0 失败** |
| 前端构建 | `npx pnpm@10.24.0 build` | exit 0（37.65s） |

**已知上游 flake**：`TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort` 在干净 HEAD worktree 与工作树均 10 跑 7 失败（失败率一致），断言点 `repo.casUpdated`，属 `ratelimit_service.go` 时序敏感既有问题，与本次改动无关。

## 二、线上冒烟（管理端价格页）

| # | 验证项 | 期望 | 实际 |
|---|---|---|---|
| 1 | `GET /health` | 200 | ✅ 200 |
| 2 | 新增模型（目录外 `test-custom-model`） | 成功返回 | ✅ 成功 |
| 3 | 重名新增 | 409 | ✅ `dup_status=409` |
| 4 | 价格覆盖写入（gpt-5.4 → 0.3） | 生效 | ✅ 回读 `input=0.3` |
| 5 | 峰谷时段 PUT/GET | 回读一致 | ✅ `09:00-18:00` 一致 |
| 6 | 峰谷双价写入（peak 0.6 / off_peak 0.3） | 生效 | ✅ `peak_enabled=true` |
| 7 | 删除自定义模型 | 200 | ✅ `delete_custom=200` |
| 8 | 删除内置模型 | 拒绝（404） | ✅ `delete_builtin=404` |
| 9 | 恢复内置 | 回退目录价 | ✅ 覆盖清空 |

## 三、模型广场端到端验证（关键）

**构造数据**：渠道 `demo-channel`（含 `deepseek-v4-flash` / `deepseek-v4-pro` 的 model_pricing）→ 分组 `demo-group`（`rate_multiplier=0.8`）→ 给 `deepseek-v4-flash` 配目录价 `2.142`（内置 0.3 的 ×7.14 还原值，即「一键还原人民币价」的语义）。

**实测输出**：

```
groups= 1
GROUP: demo-group | rate= 0.8 | catalog_count= 1
    deepseek-v4-flash | catalog_in= 2.142e-06 | pricing_in= 2.142e-06 | discounted_in= 1.7136e-06
    deepseek-v4-pro   | catalog_in= None      | pricing_in= 6.6e-07   | discounted_in= 5.28e-07
```

**数值校验**：

| 模型 | 目录价（USD/token） | 折后价 = 目录价 × 0.8 | 实测折后价 | 结论 |
|---|---|---|---|---|
| deepseek-v4-flash | 2.142e-06 | 1.7136e-06 | **1.7136e-06** | ✅ 精确匹配 |
| deepseek-v4-pro | 无（未接管） | 6.6e-07 × 0.8 = 5.28e-07 | **5.28e-07** | ✅ 未接管时回落目录价再乘倍率 |

**语义验证**：

1. **目录价优先**：`deepseek-v4-flash` 的 `catalog_in` 与 `pricing_in` 均为 2.142e-06，说明目录价已接管展示价
2. **折后价由后端计算**：`discounted_in = catalog_in × rate_multiplier`，前端可直接使用，无需二次乘倍率
3. **未纳入目录价的模型**：`deepseek-v4-pro` 的 `catalog_pricing` 为 `null`（前端据此隐藏该行的目录价列），`pricing_in` 回落 LiteLLM 目录价 6.6e-07
4. **分组统计**：`catalog_model_count = 1`（仅 `deepseek-v4-flash` 纳入目录价）

## 四、模型广场数据来源（重要说明）

模型广场的模型列表**来自渠道的 `model_pricing[].models`**，不是单独的「支持模型」字段：

- `Channel.SupportedModels()` 是**计算属性**，由 `model_pricing` 派生，不可直接写入
- 因此要让广场显示模型，必须在该渠道配置 `model_pricing`（含 models 数组）
- 只建分组、只绑渠道而不配 `model_pricing` 时，广场返回 `groups: []`

## 五、配置生效路径（运维参考）

```
1. 建渠道（platform + api_key）
2. 给渠道配 model_pricing[].models  ← 广场模型来源，缺失则广场为空
3. 建分组（rate_multiplier + channel_ids 绑定渠道）
4. 管理端「模型价格」页给模型配目录价/峰谷 ← 决定展示价与计费价
5. 访问 /model-plaza 查看：目录价 · 分组倍率 · 折后价 · 官方参考价 · 峰时价
```

## 六、遗留事项

| 项 | 说明 |
|---|---|
| 任务板 t26 状态 | 因依赖 `t24`（failed 且不可变）与 `edit_plan` 被历史任务 t1 校验阻塞，无法标记 completed；**代码已落盘、验证通过、已部署上线**，属看板状态问题，不影响交付 |
| 模型切换 | 用户要求团队改 kimi-k3.1-pro，但当前环境仅 `workbuddy/deepseek-v4.1-flash` 可用，需在 DSH 配置添加 provider 后再切换 |
| 测试数据 | 服务器上留有 `demo-channel` / `demo-group` / `test-custom-model`（已删除）等演示数据，可按需清理 |
| SMTP | 未配置，工单回复邮件会跳过（`email_sent=false`，不影响回复落库） |
