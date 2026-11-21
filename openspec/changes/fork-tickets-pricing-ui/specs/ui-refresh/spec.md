# ui-refresh

## ADDED Requirements

### Requirement: 设计令牌与配色重构
系统 SHALL 通过统一设计令牌重构全站配色（亮色/暗色），令牌包括 primary 色阶、暗色表面色阶、渐变、阴影、焦点环与选中色；SHALL 保持既有工具类名语义（`primary-*`/`dark-*`/`accent-*`）以兼容存量组件。

#### Scenario: 令牌替换生效
- **WHEN** 修改 `tailwind.config.js` 的 primary 令牌
- **THEN** 全站按钮、链接、选中态、图表主色同步变化，无需逐组件改动

#### Scenario: 对比度达标
- **WHEN** 在亮色模式查看正文与主按钮、在暗色模式查看正文与链接
- **THEN** 文本对比度均 ≥ 4.5:1，且记录在设计文档中

#### Scenario: 不引入外部字体
- **WHEN** 构建产物加载
- **THEN** 无 Google Fonts 等第三方字体请求，中文字体仍为系统栈

### Requirement: 首页重做
系统 SHALL 将默认首页重做为产品落地页（导航、Hero+代码示例、能力矩阵、模型矩阵、计费 CTA、页脚），并保留自定义首页内容与紧凑首页两种覆盖模式。

#### Scenario: 默认首页
- **WHEN** 未配置自定义首页且未启用紧凑模式
- **THEN** 展示新版落地页，CTA 按登录态指向 `/login` 或 `/dashboard`，模型广场入口按开关显示

#### Scenario: 覆盖模式不受影响
- **WHEN** 管理员配置了自定义首页 HTML
- **THEN** 仍优先渲染自定义内容，不做改动

#### Scenario: 响应式
- **WHEN** 视口为 375px / 768px / 1024px / 1440px
- **THEN** 无横向滚动、导航可用、CTA 可见

### Requirement: 设计交付物
设计任务 SHALL 输出 `design-system/sub2api/MASTER.md`（含三方向对比与选中方向的令牌表）与 `docs/design/ui-palette-preview.html`（三方向可视化预览）。

#### Scenario: 预览页可独立打开
- **WHEN** 直接用浏览器打开预览页
- **THEN** 无需网络与构建即可看到三方向色板与组件样例