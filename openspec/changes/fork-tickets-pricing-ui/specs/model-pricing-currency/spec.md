# model-pricing-currency

## ADDED Requirements

### Requirement: 人民币口径开关
系统 SHALL 提供 `convert` 与 `direct` 两种人民币计费口径：`convert` 按 `USD = CNY / cny_per_usd` 折算（默认汇率 7.14）；`direct` 直接取人民币数字作为美元（¥1=$1）。配置 SHALL 持久化在 `cny-overrides.json` 并热生效。

#### Scenario: 直接口径
- **WHEN** 模式为 `direct`，`kimi-k2` 登记输入价 ¥4/1M tokens，计费 1M 输入 token
- **THEN** 成本为 4 USD（误差 < 1e-9）

#### Scenario: 折算口径
- **WHEN** 模式为 `convert`、汇率为 7.14，同一模型计费 1M 输入 token
- **THEN** 成本为 4/7.14 USD（误差 < 1e-9）

#### Scenario: 非登记模型不受影响
- **WHEN** 切换口径
- **THEN** 未登记人民币价的模型（如 `gpt-5.4`）价格不变

### Requirement: 人民币价格登记
管理员 SHALL 能为任意模型登记人民币原生价（输入/输出/缓存读/缓存写/图片输入，CNY / 1M tokens），并可删除登记。

#### Scenario: 登记目录外模型
- **WHEN** 管理员为不在 LiteLLM 目录的模型登记人民币价
- **THEN** 该模型可被正常计费，来源标记为人民币登记

#### Scenario: USD 覆盖优先
- **WHEN** 同一模型既有人民币登记又有 USD 覆盖
- **THEN** 生效价为 USD 覆盖值

#### Scenario: 损坏文件降级
- **WHEN** `cny-overrides.json` 内容非法
- **THEN** 启动不失败、其它模型价格不受影响，管理页返回明确错误提示

### Requirement: 口径管理界面
管理端 SHALL 提供人民币口径页：模式切换、汇率输入（仅折模式可编辑）、登记表增删、已知模型导入、两种口径下的生效价对比预览。

#### Scenario: 切换口径后预览
- **WHEN** 管理员切换为 `direct`
- **THEN** 页面展示 `kimi-k2` 输入价由 0.5602 变为 4.0000 USD/1M tokens，并提示需要保存后生效