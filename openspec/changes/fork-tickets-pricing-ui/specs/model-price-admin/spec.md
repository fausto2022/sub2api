# model-price-admin

## ADDED Requirements

### Requirement: 查看内置模型价格
管理员 SHALL 可查看系统内置模型价格快照（LiteLLM 目录 + 兜底 + 覆盖 + 人民币层合成后的有效价），以 USD / 1M tokens 展示，并支持按模型名/供应商搜索与分页。

#### Scenario: 列表与搜索
- **WHEN** 管理员搜索 `kimi` 并按"已覆盖"筛选
- **THEN** 仅返回命中的模型，包含输入/输出/缓存读/缓存写价格、来源（catalog/fallback）、是否已覆盖

#### Scenario: 未配置覆盖文件
- **WHEN** `pricing.override_file` 为空
- **THEN** 列表仍可查询，编辑与恢复操作禁用并提示需配置写回路径

### Requirement: 修改与恢复内置价格
管理员 SHALL 能对单个模型写入字段级 USD 覆盖（`null` 表示移除该字段覆盖），写回 `pricing.override_file` 并热生效；SHALL 能删除某模型全部覆盖以恢复内置价。

#### Scenario: 修改输入价
- **WHEN** 管理员将 `gpt-5.4` 输入价设为 3 USD/1M tokens
- **THEN** 响应返回 `overridden=true`，随后计费按 3 USD/1M tokens 计算
- **AND** 未提交的字段保持目录值

#### Scenario: 字段级恢复
- **WHEN** 管理员提交 `output_usd_per_mtok=null`
- **THEN** 该模型输出价回退目录值，输入价覆盖保持不变

#### Scenario: 非法价格
- **WHEN** 提交负数或超大数值
- **THEN** 返回 `400 INVALID_MODEL_PRICE`，文件不变

### Requirement: 管理员显式价优先于强制官方价
当模型存在管理员覆盖或人民币登记时，系统 SHALL 不再用 DeepSeek 官方价强制覆盖该模型。

#### Scenario: 覆盖 DeepSeek 价格
- **WHEN** 管理员覆盖 `deepseek-v4-pro` 输入价并触发一次计费
- **THEN** 成本按覆盖价计算，峰谷倍率仍按请求时点叠加

#### Scenario: 未覆盖的 DeepSeek 模型
- **WHEN** 计费 `deepseek-v4-flash` 且无管理员配置
- **THEN** 行为与变更前一致（官方价 + 峰谷倍率）