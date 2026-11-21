# sub2api · UI 设计系统 MASTER（方向 B「Indigo & Cyan」）

> 本文是 **sub2api fork 全站配色的唯一事实来源（Source of Truth）**。
> 所有前端实现（首页 / 工单 / 模型价格 / 既有页面改造）都以本文的令牌与语义为准；
> 页面级差异写到 `design-system/sub2api/pages/<page>.md`，页面文件覆盖 MASTER 同名条目。
>
> | 项 | 值 |
> |---|---|
> | 版本 | v1.0（2026-11-14） |
> | 负责人 | ui-lead |
> | 默认方向 | **B 靛青 Indigo & Cyan** |
> | 令牌实现位置 | `frontend/tailwind.config.js`（`primary` / `dark` / `accent` / 渐变 / 阴影 / 动效）与 `frontend/src/style.css`（`:root` CSS 变量、`.btn-*` / `.input-*` / `.card*` / `.badge-*`、焦点环、滚动条、`tabular-nums`） |
> | 可视化预览 | `docs/design/ui-palette-preview.html`（纯静态、零外部依赖，浏览器直接打开） |
> | 依据 | `openspec/changes/fork-tickets-pricing-ui/design.md` §4.1/§4.2、`tasks.md` §0；ui-ux-pro-max `--design-system` 输出 |

## 0. 令牌契约（前端所有成员必须遵守）

1. **类名语义不变**：`primary-*` 恒为品牌主色、`dark-*` 恒为暗色表面、`accent-*` 恒为中性灰阶。
   本次重构**只换令牌数值**，不改任何工具类名 —— 业务组件无需改名即可换肤。
2. **禁止在业务组件里写裸 hex / rgb**；只用令牌工具类（`bg-primary-600`、`text-accent-600`、`border-dark-700`…）。
3. **暗色不是反色**：暗色用独立的 ink 色阶（`dark-*`），与亮色语义分别调校（skill `color-dark-mode`）。
4. 新增语义色（成功/警告/危险/信息）已内置为单值工具类：`text-success` / `bg-danger` / `border-warning` / `text-info`；
   需要深浅阶时用 Tailwind 原生 `emerald-*` / `amber-*` / `red-*` / `cyan-*`（下表已给出合规取值）。
5. 数字列（价格、用量、统计、时间）一律加 `tabular-nums`。

## 1. 设计流程与 skill 依据

### 1.1 实际执行的查询

```bash
python C:\Users\ThinkPad\.agents\skills\ui-ux-pro-max\scripts\search.py \
  "AI API gateway developer platform SaaS dashboard" \
  --design-system --density 7 --variance 5 --motion 4 -f markdown
```

输出要点（保留原样判定，便于复核）：

| 维度 | skill 输出 | 处置 |
|---|---|---|
| Pattern | FAQ/Documentation Landing（Hero+搜索 → 分类 → FAQ → 联系 CTA） | **不采纳**为首页 IA：首页信息架构由 `design.md` §4.3（导航/Hero+代码示例/能力矩阵/模型墙/计费 CTA/页脚）定义；仅借鉴"Hero 主 CTA 明确、单一转化焦点"的纪律 |
| Style | Minimalism & Swiss Style（干净、留白、几何、网格、高对比、无装饰） | **采纳**：网格化布局、克制的色彩用量、清晰层级、系统无衬线字 |
| Colors | 深蓝底 `#0F172A/#020617` + 端点绿 `#22C55E` | **不采纳色板**：色板由 `design.md` §4.2 定为三方向，默认 B 靛青；skill 的"暗色代码底 + 单一强调色"结构保留（B 的强调色 = cyan `#06B6D4`） |
| Typography | JetBrains Mono + IBM Plex Sans（Google Fonts CDN） | **不采纳字体下载**：硬约束禁止外部字体 CDN。保留**系统字体栈**（`system-ui`/`PingFang SC`/`Microsoft YaHei`）+ `ui-monospace` 作代码与等宽数字；"技术感"靠等宽数字与几何留白表达 |
| Key Effects | 200–250ms hover、柔和过渡、清晰层级、快速加载 | **采纳**为动效令牌（§3.9） |
| Motion(4/10) | Stagger List，300–450ms，`back.out(1.4)`，40–60ms 步进，reduced-motion 下直接终态 | **采纳**用于列表/卡片入场；**不用于**密集数据表格（skill 明确警示：overshoot 在信息型 UI 上显得草率） |
| Anti-patterns | 端点埋没、版本切换断裂、缺限流态 | 纳入验收关注点（首页/价格页必须显示 base URL、限流/配额状态） |
| Pre-Delivery Checklist | 无 emoji 图标、可点击元素 `cursor-pointer`、hover 过渡、亮色文字 ≥4.5:1、键盘焦点可见、reduced-motion、375/768/1024/1440 | 全部纳入 §6 验收 |

### 1.2 读取的参考条目（`references/quick-reference.md` §6、`references/pro-rules.md` Light/Dark Mode Contrast）

| 规则 | 要求 | 落地 |
|---|---|---|
| `color-dark-mode` | 暗色用**去饱和/更亮的同调变体**，不是亮色反色；对比度必须独立测量 | 独立 ink 色阶（§3.4）；亮暗两套语义表（§3.1/§3.2）；对比度分别实测（§5） |
| `color-accessible-pairs` | 前景/背景配对 ≥4.5:1（AA），大字/非文本 ≥3:1 | §5 全部实测并给出通过/不通过；未达标项已修（§7.2 的 3 处偏离） |
| `color-semantic` | 组件用语义令牌，不写裸 hex | §0.2 |
| `number-tabular` | 数据列用等宽数字防跳动 | `.tabular-nums` + `.table` 默认启用 |
| `color-not-decorative-only` | 功能色必须配图标/文字，不能只靠颜色 | 工单状态徽章 = 文字+色；危险/成功不单靠色块 |
| `visual-hierarchy` | 层级靠尺寸/间距/对比，不靠颜色堆叠 | §3.7~§3.10 |
| 亮暗独立测量（pro-rules） | "Checked dark mode contrast independently" | §5 分亮/暗两节 |
| 焦点可见 | 移除焦点环属违规 | 全局 `focus-visible` 双 token 环（§4.1） |

## 2. 三个方向对比

三方向**只允许替换令牌**（`tailwind.config.js` 里的 `primary`/`dark` 两组常量 + `style.css` 的 `--ui-*`），组件与类名不动。

| 令牌 | A 玉青 Jade Circuit（延续品牌） | **B 靛青 Indigo & Cyan（默认）** | C 石墨青柠 Graphite & Lime |
|---|---|---|---|
| primary-500 | `#17A88A` | `#6366F1` | `#334155` |
| primary-600 | `#0C8A73` | `#4F46E5` | `#1E293B` |
| primary-700 | `#0C6E5D` | `#4338CA` | `#0F172A` |
| 强调/数据色 | `#0284C7` 天蓝 | `#06B6D4` 青 | `#84CC16` 青柠 |
| 成功 / 警告 / 危险 | `#059669` / `#D97706` / `#DC2626` | `#10B981` / `#F59E0B` / `#EF4444` | `#22C55E` / `#EAB308` / `#EF4444` |
| 亮色背景 / 卡片 | `#F7FAF9` / `#FFFFFF` | `#F6F7FB` / `#FFFFFF` | `#FAFAF9` / `#FFFFFF` |
| 暗色背景 / 卡片 | `#06110F` / `#0C1A17` | `#070B14` / `#0E1424`（实现取 ink：`#070B14` / `#131C31`） | `#0B0C0B` / `#141615` |
| 暗色边框 | `#1B2E29` | `#1E2740` | `#262B26` |
| 气质 | 稳健、延续现有品牌（青绿） | 现代开发者 SaaS、蓝紫信任感、对比强 | 终端/极客、差异化最强、最中性 |
| 白字主按钮实测 | `#0C8A73` 上白字 **4.29:1 不达标** → 需压深到 `#0A7F6A`（4.93:1） | primary-600 `#4F46E5` **6.29:1** ✅；primary-700 **7.90:1** ✅ | `#334155` **10.35:1** ✅（对比度最安全） |
| 正文/链接实测 | `#0C8A73` 作链接 on white **4.29:1 不达标** → 用 primary-700 `#0C6E5D`（6.17:1） | primary-600 on white **6.29:1** ✅ | `#334155` on white **10.35:1** ✅ |
| 暗色正文实测 | `#5EEAD4` on `#06110F` **12.97:1** | dark-100 on `#070B14` **15.86:1** | `#A3E635` on `#0B0C0B` **13.00:1** |
| 结论 | 品牌延续性最好，但默认色阶本身不达 AA，需额外压深两档；青绿+天蓝双冷色在暗色下容易"糊" | **默认落地**：AA 全通过、蓝紫为开发者产品最常见的信任色、与 cyan 强调形成冷色层次的差异化（避免"又一个青绿 SaaS"） | 对比度最优、最中性，但主色与灰阶过近，品牌识别弱；适合"极客工具"定位迭代 |

**切换方式（不改组件）**

1. 改 `frontend/tailwind.config.js`：替换 `indigoPrimary` 与 `ink` 两个常量对象（保留键名 `primary` / `dark` / `ink`）。
2. 改 `frontend/src/style.css` `:root` / `.dark` 的 `--ui-*`（canvas / surface / surface-muted / border / text-muted / ring / selection / scrollbar）。
3. `.btn-*` 实心按钮若主色阶亮度不同，按 §4.1 的「白字 ≥4.5:1」重新选 -600/-700 档。
4. 跑 `docs/design/ui-palette-preview.html` 对照（同文件内含 A/B/C 三套令牌样例）。

## 3. 方向 B 完整令牌

### 3.1 亮色语义表（`theme.light`）

| 语义 | 令牌 | 值 | 对应类 | 用途 |
|---|---|---|---|---|
| canvas | `--ui-canvas` | `#F6F7FB` | `body` 背景 | 页面底色 |
| surface | `--ui-surface` | `#FFFFFF` | `bg-white` | 卡片 / 弹窗 / 侧栏 |
| surface-muted | `--ui-surface-muted` | `#F1F5F9` | `bg-accent-100` | 表头、tabs 槽、代码片段底 |
| border | `--ui-border` | `#E2E8F0` | `border-accent-200` | 分隔线、卡片边框 |
| border-strong | — | `#CBD5E1` | `border-accent-300` | hover 边框、可交互容器 |
| text | — | `#0F172A` | `text-accent-900` | 正文 / 标题 |
| text-secondary | — | `#475569` | `text-accent-600` | 说明文字、表格正文 |
| text-muted | `--ui-text-muted` | `#475569` | `text-accent-600` | 辅助信息（**不用 accent-500**：4.45:1 不达 AA） |
| text-inverse | — | `#FFFFFF` | `text-white` | 实心填充上的字 |
| primary | `--ui-ring`(环) | `#6366F1` | `primary-500` | 图标/装饰、进度条、选中态 |
| primary-text | — | `#4F46E5` | `text-primary-600` | 链接、可点击文字 |
| primary-fill | — | `#4F46E5 → #4338CA` | `from-primary-600 to-primary-700` | 主按钮实心 |
| focus-ring | `--ui-ring` | `#6366F1` | `ring-primary-500`（**不透明**） | 键盘焦点 |
| selection | `--ui-selection-bg` | `rgba(99,102,241,.22)` | `::selection` | 文本选中 |
| scrollbar | `--ui-scrollbar` | `rgba(148,163,184,.55)` | `::-webkit-scrollbar-thumb` | 滚动条（hover `#94A3B8`） |

### 3.2 暗色语义表（`theme.dark`，作用于 `.dark` 根类）

| 语义 | 令牌 | 值 | 对应类 | 用途 |
|---|---|---|---|---|
| canvas | `--ui-canvas` | `#070B14` | `dark-950` | 页面底 |
| surface | `--ui-surface` | `#131C31` | `dark-800` | 卡片（可带 `/60` 透明叠色） |
| surface-muted | `--ui-surface-muted` | `#0B1120` | `dark-900` | 侧栏、弹窗底、下沉区块 |
| border | `--ui-border` | `#1E2740` | `dark-700` | 分隔线、卡片边框 |
| border-strong | — | `#2C3852` | `dark-600` | hover 边框、输入框边 |
| text | — | `#E2E7F0` | `dark-100` | 正文 |
| text-strong | — | `#F5F7FB` | `dark-50` | 标题 / 强调数字 |
| text-secondary | — | `#9AA6BE` | `dark-300` | 说明文字（6.92:1 @ dark-800） |
| text-muted | `--ui-text-muted` | `#9AA6BE` | `dark-300` | 辅助信息（**不用 dark-500**：2.64:1） |
| primary | — | `#818CF8` | `primary-400` | 暗色主色文字 / 图标 / 焦点环 |
| primary-fill | — | `#4F46E5 → #4338CA` | `from-primary-600 to-primary-700` | 主按钮（暗色下不变，白字 6.29/7.90） |
| focus-ring | `--ui-ring` | `#818CF8` | `ring-primary-400` | 键盘焦点（暗底 6.60:1） |
| selection | `--ui-selection-bg` | `rgba(99,102,241,.38)` | `::selection` | 选中（配 `text-primary-100`，11.49:1） |
| scrim | — | `rgba(0,0,0,.60)` | `bg-black/60` | 弹窗遮罩（亮色用 `/40`） |

### 3.3 品牌主色 primary（靛青 Indigo，11 阶）

| 阶 | 值 | 亮色用法 | 暗色用法 |
|---|---|---|---|
| 50 | `#EEF2FF` | 徽章底、选中行底、侧栏激活底 | — |
| 100 | `#E0E7FF` | 徽章底、hover 激活底 | — |
| 200 | `#C7D2FE` | 装饰描边 | — |
| 300 | `#A5B4FC` | 装饰 | 暗色徽章字（8.51:1 @ dark-800）、暗色链接 hover |
| 400 | `#818CF8` | 大字装饰 | **暗色主文字/图标/焦点环**（6.60:1 @ dark-950，4.96:1 @ dark-700） |
| 500 | `#6366F1` | 图标、进度条、选中态、焦点环 | 徽章底 `/15` |
| 600 | `#4F46E5` | **链接/按钮文字首选**（6.29:1 白底）；主按钮渐变起点 | 主按钮渐变起点 |
| 700 | `#4338CA` | 主按钮渐变终点（7.90:1）、强调文字 | 主按钮渐变终点 |
| 800 | `#3730A3` | hover 终点、深色徽章字 | — |
| 900 | `#312E81` | 深色强调 | 徽章底 `/15`、激活底 `/20` |
| 950 | `#1E1B4B` | 阴影色源 | 深色徽章底 |

> **规则**：实心填充承载白字时只用 **600 / 700**（≥4.5:1）；**500 上的白字只有 4.47:1**，不可用于正文按钮（装饰/图标可以）。

### 3.4 暗色表面 dark（ink 色阶，11 阶，950 深 → 50 浅）

| 阶 | 值 | 用法 |
|---|---|---|
| 950 | `#070B14` | 页面底（canvas） |
| 900 | `#0B1120` | 侧栏、弹窗底、代码块底 |
| 800 | `#131C31` | 卡片面（`dark:bg-dark-800/60`）、输入框底 |
| 700 | `#1E2740` | 边框/分隔线、hover 面、tab 激活底 |
| 600 | `#2C3852` | 强边框、输入框边、关闭态开关轨 |
| 500 | `#475573` | 装饰/图标/滚动条 hover（**2.64:1，禁止作正文**） |
| 400 | `#7A88A6` | 三级文字（5.53 @950 / **4.77 @800**）；placeholder |
| 300 | `#9AA6BE` | 二级文字（8.03 @950 / 6.92 @800） |
| 200 | `#C3CCDC` | 表格正文、次级按钮字（10.49 @800） |
| 100 | `#E2E7F0` | 正文（15.86 @950 / 13.67 @800） |
| 50 | `#F5F7FB` | 标题、强调数字（15.81 @800） |

> 偏离说明见 §7.2：`400` 由 `#6E7C9A` 提亮到 `#7A88A6`。

### 3.5 语义色

| 语义 | 文字（亮 / 暗） | 实心填充（白字） | 浅底（亮） | 浅底（暗） | 用法 |
|---|---|---|---|---|---|
| 成功 success | `text-emerald-700` (5.48:1) / `dark:text-emerald-300` (11.13:1) | `emerald-700→800` (5.48 / 7.68) | `bg-emerald-50` (5.21) | `emerald-500/15` | 已解决工单、余额充足、保存成功 |
| 警告 warning | `text-amber-700` (5.02:1) / `dark:text-amber-300` (11.76:1) | `amber-400→500` + **深字** `dark-900` (11.28 / 8.77) | `bg-amber-50` (4.84) | `amber-500/15` | 待处理、临近配额、风险提示 |
| 危险 danger | `text-red-700` (6.47:1) / `dark:text-red-300` (8.94:1) | `red-600→700` (4.83 / 6.47) | `bg-red-50` (5.91) | `red-500/15` | 删除、失败、超额 |
| 信息 info | `text-cyan-700` (5.36:1) / `dark:text-cyan-300` | `cyan-700` (5.36 白字) | `bg-cyan-50` (5.15) | `cyan-500/15` | 提示、API 端点、新功能 |
| 中性 | `text-accent-600` (7.06) / `dark:text-dark-300` | `accent-700` (9.9 白字) | `bg-accent-100` (9.45) | `dark-700` | 默认徽章、禁用态 |

> `cyan-600 #0891B2` 在白底只有 3.68:1 → **仅用于图形/描边**，不用于文字。

### 3.6 渐变

| 令牌 | 值 | 用法 / 限制 |
|---|---|---|
| `bg-gradient-primary` | `linear-gradient(135deg,#6366F1 0%,#06B6D4 100%)` | Hero 标题强调、装饰条、CTA 背景；**不承载正文白字**（起点 4.47:1） |
| `bg-gradient-dark` | `linear-gradient(135deg,#131C31 0%,#070B14 100%)` | 暗色卡片/页脚、模型墙容器 |
| `bg-gradient-glass` | 白色 10%→5% | 玻璃卡片高光 |
| `bg-mesh-gradient` | indigo 14% / cyan 10% 双光斑 | 首页 Hero、落地页背景（低透明度，不做内容底） |
| `text-gradient` | `primary-600 → primary-400 → cyan-400` | 仅用于 ≥24px 标题（大字阈值下对比度按 3:1 判定） |

### 3.7 阴影

| 令牌 | 值 | 用法 |
|---|---|---|
| `shadow-card` | `0 1px 3px rgba(7,11,20,.04), 0 1px 2px rgba(7,11,20,.06)` | 卡片默认（暗色下靠边框区分，阴影几乎不可见） |
| `shadow-card-hover` | `0 10px 40px rgba(7,11,20,.08)` | 卡片 hover 抬升 |
| `shadow-glass` / `shadow-glass-sm` | `0 8px 32px` / `0 4px 16px` 黑 8% / 6% | 玻璃层、悬浮面板 |
| `shadow-glow` | `0 0 20px rgba(99,102,241,.28)` | 品牌聚焦元素（主 CTA hover、激活 tab） |
| `shadow-glow-lg` | `0 0 40px rgba(99,102,241,.38)` | Hero CTA、模型墙高亮 |
| `shadow-inner-glow` | `inset 0 1px 0 rgba(255,255,255,.1)` | 暗色卡片顶部高光（1px） |

### 3.8 圆角

| 令牌 | 值 | 用途 |
|---|---|---|
| `rounded-md` | 6px | 提示条、标签 |
| `rounded-lg` | 8px | 小按钮（`btn-sm`）、tab、输入前缀块 |
| `rounded-xl` | 12px | **默认**：按钮、输入框、下拉、表格容器、toast |
| `rounded-2xl` | 16px | 卡片、弹窗、侧栏项、大按钮 |
| `rounded-3xl` | 24px | Hero 面板、模型墙分组容器 |
| `rounded-4xl`（自定义） | 32px | 营销区块（首页 Hero 外层） |
| `rounded-full` | 9999px | 徽章、头像、开关轨、进度条 |

### 3.9 间距与栅格（density 7 → 面板类留白）

| 令牌 | 值 | 用途 |
|---|---|---|
| `1` / `1.5` | 4 / 6px | 图标与文字、徽章内微距 |
| `2` / `2.5` | 8 / 10px | 紧凑堆叠（表格单元内） |
| `3` / `4` | 12 / 16px | 控件内边距、列表项间距 |
| `5` / `6` | 20 / 24px | 卡片内边距（`p-5`/`p-6`）、区块间隔 |
| `8` / `10` | 32 / 40px | 区块纵向间距（面板类） |
| `16` / `20` | 64 / 80px | 首页区块纵向留白（营销类，仅首页用） |

栅格：内容容器上限 `max-w-7xl`（1280px），面板页 `max-w-[1440px]`；断点 375（单列）/768（2 列）/1024（侧栏常驻）/1440（3–4 列）。

### 3.10 字阶（系统字体栈 + 等宽数字）

| 令牌 | 字号/行高 | 字重 | 用途 |
|---|---|---|---|
| `text-xs` | 12 / 16 | 500 | 徽章、表头标签、辅助说明 |
| `text-sm` | 14 / 20 | 400–500 | **正文默认**、表单、表格、按钮 |
| `text-base` | 16 / 24 | 400 | 长文正文、弹窗正文 |
| `text-lg` | 18 / 28 | 600 | 卡片标题、弹窗标题 |
| `text-xl` | 20 / 28 | 600 | 区块标题 |
| `text-2xl` | 24 / 32 | 700 | 页面标题（`page-title`）、统计值 |
| `text-3xl` | 30 / 36 | 700 | 首页次级标题 |
| `text-4xl` | 36 / 40 | 700–800 | 首页 Hero 标题（≥1024 用 `text-5xl` 48/56） |

字体族：`font-sans` = 系统栈（含 PingFang SC / Microsoft YaHei）；`font-mono` = `ui-monospace` 栈（代码、密钥、等宽数字）。
数字：价格/用量/统计用 `tabular-nums` + `font-mono`（API 密钥、模型名同样用 mono）。

### 3.11 图标与插画

- 尺寸令牌：16（行内）、20（按钮）、24（导航/空态）；描边 1.5px（16/20）/ 2px（24），同层统一。
- 只用 SVG 图标（项目已用 `@lobehub/icons` 与内联 SVG）；**禁止 emoji 当图标**。
- 有意义的图标 ≥3:1 对比（亮色 `text-accent-500`、暗色 `dark:text-dark-400`）；纯装饰图标 `aria-hidden="true"`。

### 3.12 动效令牌

| 场景 | 时长 / 缓动 |
|---|---|
| hover 颜色、边框 | 150ms `ease-out` |
| 按钮/卡片过渡（默认） | 200ms `ease-out` |
| 弹窗进入 / 离开 | 250ms `ease-out` / 200ms `ease-in`（离开更快） |
| 卡片抬升、页面区块 | 300ms `ease-out` |
| 列表/网格入场 | 300–450ms，`stagger 40ms`，`back.out(1.4)`（**不用于数据表格**） |
| active 反馈 | `scale(0.98)`，≤120ms |

`prefers-reduced-motion: reduce` 时：全局动画/过渡 `0.01ms`、迭代 1 次、`scroll-behavior: auto`（`style.css` base 层已兜底，弹窗另有局部规则）。

## 4. 组件规范与实测

### 4.1 按钮矩阵

| 类 | 亮色填充 | 字色 | 实测对比度 | 暗色 |
|---|---|---|---|---|
| `.btn-primary` | `primary-600 → 700` | `#FFFFFF` | **6.29 / 7.90** ✅ | 同（`dark:shadow-primary-950/40`） |
| `.btn-secondary` | `#FFFFFF`（边 `accent-200`） | `accent-700` | 9.67 ✅ | `dark-800` + 边 `dark-600` + 字 `dark-200` (10.49 ✅) |
| `.btn-ghost` | 透明（hover `accent-100`） | `accent-600` | 7.06 ✅ | 字 `dark-300`（6.92 ✅） |
| `.btn-danger` | `red-600 → 700` | `#FFFFFF` | **4.83 / 6.47** ✅ | 同 |
| `.btn-success` | `emerald-700 → 800` | `#FFFFFF` | **5.48 / 7.68** ✅ | 同 |
| `.btn-warning` | `amber-400 → 500` | `dark-900 #0B1120` | **11.28 / 8.77** ✅ | 同（深字浅底） |
| `.btn-stripe` / `.btn-alipay` / `.btn-wxpay` / `.btn-airwallex` | 品牌官方色 | 白/品牌 | 例外 E1（见 §6） | 品牌暗色变体 |

**焦点环（全部可交互元素统一）**：`focus-visible:ring-2 ring-primary-500 ring-offset-2 ring-offset-white`，暗色 `dark:ring-primary-400 dark:ring-offset-dark-950`。
不用 `ring-primary-500/60`：叠色后对白底仅 **2.31:1**（非文本要求 ≥3:1）。不透明 primary-500 对白底 4.47:1、primary-400 对暗底 6.60:1，均通过。

### 4.2 输入框

`.input`：底 `#FFFFFF` / `dark-800`，边 `accent-200` / `dark-600`，字 `accent-900` / `dark-100`，placeholder `accent-400` / `dark-400`；
focus：`border-primary-500` + `ring-2 ring-primary-500/40`（边框本身即 4.47:1 的可见焦点提示），暗色 `border-primary-400`；
错误态 `.input-error`：`border-red-500` + `ring-red-500/30`，并必须同时给 `.input-error-text`（不靠颜色单独表达）。
原生 `date/time` 等控件在暗色下 `color-scheme: dark`（既有规则保留）。

### 4.3 卡片 / 面板

`.card`：底 `#FFFFFF` / `dark-800/60`，边 `accent-200/70` / `dark-700/60`，`rounded-2xl`，`shadow-card`。
亮色 canvas 与 card 的对比度只有 **1.07:1**（纯表面差），**因此卡片必须有边框或阴影**，不能靠"底色差"分区（暗色同理，靠 `dark-700` 边框）。
`.card-hover`：`-translate-y-0.5` + `shadow-card-hover` + 边框升到 `accent-300` / `dark-600`。

### 4.4 徽章（状态/标签）

`.badge` 已加 `whitespace-nowrap`（skill `compact-label-overflow`：标签不换行、可横向溢出由容器处理）。
亮色：`bg-*-50` + `text-*-700`（实测 4.84–9.45 ✅）；暗色：`bg-*-500/15` + `text-*-300`（8.51–11.76 ✅）。
工单状态映射：open=`badge-primary`、pending=`badge-warning`、resolved=`badge-success`、closed=`badge-gray`；优先级 urgent=`badge-danger`、high=`badge-warning`、normal=`badge-gray`、low=`badge-gray`。
文字必须存在（`color-not-decorative-only`），不得只靠色点。

### 4.5 表格

`.table` 默认 `tabular-nums`；表头 `text-accent-600`/`dark-300`，正文 `text-accent-700`/`dark-200`；
行 hover `bg-accent-50`/`dark-800/30`；容器 `rounded-xl border`。
价格页/用量页的数值列另加 `font-mono`（USD 4 位小数、CNY 2 位小数），右对齐。

### 4.6 其它

- `.tabs`/`.tab`：槽 `accent-100`/`dark-800`，激活 `#FFFFFF`/`dark-700`，字 `accent-900`/`dark-50`，`focus-visible` 环。
- `.switch`：轨 `accent-300`/`dark-600`，激活 `primary-500`，thumb 白色；状态以**位置 + 颜色**双通道表达。
- `.modal-*` / `.dialog-*`：面 `#FFFFFF`/`dark-800`，头尾边 `accent-200`/`dark-700`，遮罩亮 `black/40`、暗 `black/60`。
- `.toast`：面 `#FFFFFF`/`dark-800`，左侧 4px 语义色条（success/error/warning/info），文案同时给文字。
- `.sidebar`：亮 `#FFFFFF` + 右边 `accent-200`；暗 `dark-900` + 右边 `dark-700`；激活项 `primary-50`/`primary-900/20` + `primary-600`/`primary-400`（暗色 6.6:1）。

## 5. 对比度实测表

方法：WCAG 2.1 相对亮度（sRGB 线性化），`ratio = (L_light+0.05)/(L_dark+0.05)`；脚本见附录 A，可复现。
判定：正文/按钮文字 **AA ≥4.5:1**；大字（≥24px 或 ≥18.66px 粗体）与图形/焦点环 ≥3:1。

### 5.1 亮色（light）

| # | 配对 | 比值 | 判定 |
|---|---|---|---|
| 1 | 正文 `accent-900 #0F172A` on canvas `#F6F7FB` | **16.68:1** | ✅ |
| 2 | 正文 `accent-900` on card `#FFFFFF` | **17.85:1** | ✅ |
| 3 | 次正文 `accent-600 #475569` on `#FFFFFF` | **7.56:1** | ✅ |
| 4 | 次正文 `accent-600` on `#F6F7FB` | **7.06:1** | ✅ |
| 5 | `accent-500 #64748B` on `#F6F7FB` | 4.45:1 | ❌（→ 仅用于白色卡片，或改用 accent-600） |
| 6 | 链接 `primary-600 #4F46E5` on `#FFFFFF` | **6.29:1** | ✅ |
| 7 | 链接 `primary-600` on `#F6F7FB` | **5.87:1** | ✅ |
| 8 | `primary-500 #6366F1` 作正文 on `#F6F7FB` | 4.17:1 | ❌（→ 正文用 600/700，500 只做图形） |
| 9 | 主按钮白字 on `primary-500` | 4.47:1 | ❌（→ 实心填充改用 600/700） |
| 10 | 主按钮白字 on `primary-600` | **6.29:1** | ✅ |
| 11 | 主按钮白字 on `primary-700` | **7.90:1** | ✅ |
| 12 | 徽章 `primary-700` on `primary-50 #EEF2FF` | **7.07:1** | ✅ |
| 13 | 徽章 `primary-700` on `primary-100 #E0E7FF` | **6.41:1** | ✅ |
| 14 | 危险按钮白字 on `red-600 #DC2626` | **4.83:1** | ✅ |
| 15 | 危险按钮白字 on `red-500 #EF4444`（旧值） | 3.76:1 | ❌（已替换） |
| 16 | 成功按钮白字 on `emerald-700 #047857` | **5.48:1** | ✅ |
| 17 | 成功按钮白字 on `emerald-500 #10B981`（旧值） | 2.54:1 | ❌（已替换） |
| 18 | 警告按钮 `dark-900` on `amber-400 #FBBF24` | **11.28:1** | ✅ |
| 19 | 警告按钮 `dark-900` on `amber-500 #F59E0B` | **8.77:1** | ✅ |
| 20 | 警告按钮白字 on `amber-500`（旧值） | 2.15:1 | ❌（已替换为深字浅底） |
| 21 | 焦点环 `primary-500` on `#FFFFFF` | **4.47:1** | ✅ (≥3) |
| 22 | 焦点环 `primary-500/60` on `#FFFFFF`（旧写法） | 2.31:1 | ❌（已改为不透明） |
| 23 | 选中文本 `primary-900` on `rgba(99,102,241,.22)` over canvas | **7.82:1** | ✅ |
| 24 | 错误文字 `red-700` on `red-50` | **5.91:1** | ✅ |
| 25 | 警告文字 `amber-700` on `amber-50` | **4.84:1** | ✅ |
| 26 | 成功文字 `emerald-700` on `emerald-50` | **5.21:1** | ✅ |
| 27 | 信息文字 `cyan-700 #0E7490` on `#FFFFFF` | **5.36:1** | ✅ |
| 28 | canvas vs card（表面差，非文字） | 1.07:1 | 说明：必须靠边框/阴影分区 |

### 5.2 暗色（dark，ink 色阶）

| # | 配对 | 比值 | 判定 |
|---|---|---|---|
| 1 | 正文 `dark-100 #E2E7F0` on canvas `dark-950 #070B14` | **15.86:1** | ✅ |
| 2 | 正文 `dark-100` on card `dark-800 #131C31` | **13.67:1** | ✅ |
| 3 | 次正文 `dark-300 #9AA6BE` on `dark-950` | **8.03:1** | ✅ |
| 4 | 次正文 `dark-300` on `dark-800` | **6.92:1** | ✅ |
| 5 | 三级文字 `dark-400 #7A88A6` on `dark-950` | **5.53:1** | ✅ |
| 6 | 三级文字 `dark-400` on `dark-900` | **5.29:1** | ✅ |
| 7 | 三级文字 `dark-400` on `dark-800` | **4.77:1** | ✅ |
| 8 | `dark-400` on `dark-700 #1E2740` | 4.16:1 | ❌（dark-700 面上用 dark-300） |
| 9 | `dark-500 #475573` 作正文 on `dark-950` | 2.64:1 | ❌（仅装饰） |
| 10 | 链接/主色 `primary-400 #818CF8` on `dark-950` | **6.60:1** | ✅ |
| 11 | `primary-400` on `dark-800` | **5.69:1** | ✅ |
| 12 | `primary-400` on `dark-700` | **4.96:1** | ✅ |
| 13 | `primary-300 #A5B4FC` on `dark-950` | **9.87:1** | ✅ |
| 14 | 徽章 `primary-300` on `primary-900/15` over `dark-950` | **8.81:1** | ✅ |
| 15 | 徽章 `primary-400` on `primary-900/15` over `dark-800` | **5.16:1** | ✅ |
| 16 | 主按钮白字 on `primary-600`（暗色同用） | **6.29:1** | ✅ |
| 17 | 焦点环 `primary-400` on `dark-950` | **6.60:1** | ✅ (≥3) |
| 18 | 选中文本 `primary-100` on `rgba(99,102,241,.38)` over `dark-950` | **11.49:1** | ✅ |
| 19 | `dark-400` 作为 placeholder（非必需 4.5） | 5.53:1 | ✅（优于常规容忍） |
| 20 | 卡片 `dark-800` vs canvas `dark-950`（表面差） | 1.35:1 | 说明：靠 `dark-700` 边框分区 |

### 5.3 三方向主按钮横向对比

| 方向 | 填充 | 白字比值 | 判定 |
|---|---|---|---|
| A | `#0C8A73` | 4.29:1 | ❌ → 压深到 `#0A7F6A`（**4.93:1** ✅） |
| **B（默认）** | `#4F46E5 → #4338CA` | **6.29 / 7.90:1** | ✅ |
| C | `#334155` | **10.35:1** | ✅ |

## 6. 例外与已知限制

| 编号 | 项 | 实测 | 处理与理由 |
|---|---|---|---|
| E1 | 品牌支付按钮（Stripe `#635BFF`、支付宝 `#00AEEF`、微信 `#2BB741`、Airwallex `#14171A`） | 白字 4.70 / 2.53 / 2.64 / 17.99 | 保留品牌官方色：支付渠道品牌识别与合规要求优先于 AA；按钮文字 ≥14px 且加粗，并在同一行给出纯文本说明（不单靠颜色传达状态） |
| E2 | 静态分隔线 / 卡片边框 | 亮 `#E2E8F0` on `#F6F7FB` 1.15:1；暗 `dark-700` on `dark-950` 1.33:1 | 装饰性分区不承载状态信息，不适用 1.4.11；**控件边界**由 focus 态（≥3:1）与语义色（error/success）承担 |
| E3 | Hero 渐变标题 `text-gradient` | 中间色随位置变化 | 仅用于 ≥24px 大字（大字阈值 3:1），不用于正文；正文另用实色 |
| E4 | `gradient-primary` 起点 `#6366F1` | 白字 4.47:1 | 该渐变**只做装饰**（背景光斑、装饰条、进度），任何承载正文/按钮文字的面改用 `primary-600/700` |

## 7. 禁用项（Do Not）

1. **禁止引入外部字体 CDN**（Google Fonts / Adobe Fonts / jsDelivr 字体包）——中文字体走系统栈。
2. **禁止 emoji 当图标**（✅❌⚠️ 等）；状态用 SVG + 文字。
3. **禁止在组件中写裸 hex / `rgb()`**；只能用令牌工具类。
4. **禁止把暗色做成亮色反色**：暗色必须用 `dark-*` 独立色阶，且亮/暗对比度**分别**测量。
5. **禁止移除焦点环**或把 `outline: none` 单留；所有可交互元素必须有 `focus-visible` 环。
6. **禁止用 `primary-500` / `primary-400` 作正文按钮背景并配白字**（4.47:1）；实心填充只用 600/700。
7. **禁止用低透明度焦点环**（`/60`、`/50`）替代不透明环（叠色后 <3:1）。
8. **禁止仅靠颜色表达状态**（错误/成功/工单状态必须带文字或图标）。
9. **禁止在 `dark-700` 背景上用 `dark-400` 作正文**（4.16:1）；用 `dark-300`。
10. **禁止在 Tailwind 配置里改名/删除既有 `animation`/`keyframes`**（组件已在用），只允许调色。
11. **禁止用 `!important` 或行内 style 覆盖令牌**解决个别页面；需要新语义时在 MASTER 增补令牌。
12. 禁止把不可验证的数字（如"99.99% 可用性"）作为设计装饰。

## 8. 迁移指引（组件作者速查）

| 旧写法 | 新写法 | 原因 |
|---|---|---|
| `text-gray-900` / `bg-gray-50` | `text-accent-900` / `bg-accent-50`（页面底直接由 body 提供） | 中性色统一为 accent（slate） |
| `dark:text-white` | `dark:text-dark-50` | 纯白在暗色下发刺，且脱离 ink 体系 |
| `dark:text-dark-500`（正文/说明） | `dark:text-dark-400`（≥4.77:1）或 `dark-300` | 500 仅装饰 |
| `text-primary-500`（正文/链接） | `text-primary-600 dark:text-primary-400` | 500 白底仅 4.17:1 |
| `bg-primary-500 text-white`（按钮） | `.btn-primary` 或 `bg-primary-600 text-white` | 白字需 ≥4.5 |
| `focus:ring-2 ring-primary-500/50` | `focus-visible:ring-2 ring-primary-500`（暗 `dark:ring-primary-400`） | 不透明度与焦点可见性 |
| `bg-red-500` / `bg-emerald-500` 实心按钮 | `red-600/700` / `emerald-700/800`（警告用深字浅底） | 白字对比度 |
| 价格/用量列 | 加 `tabular-nums`（表格已默认） | 防数字跳动 |

## 9. 验证与产物

| 项 | 结果 |
|---|---|
| `node --input-type=module -e "import('./tailwind.config.js')"` | ✅ 配置可解析；`primary.500=#6366F1`、`dark.400=#7A88A6`、`accent.500=#64748b`；animation/keyframes/backgroundImage 键名与改动前一致（8/7/5） |
| `npx pnpm@10.24.0 build`（含 `check:i18n` + `vue-tsc` + `vite build`） | 见交付报告（本次仅令牌层改动；失败项按 tasks.md §0.6 只修令牌层） |
| 产物清单 | `design-system/sub2api/MASTER.md`（本文）、`docs/design/ui-palette-preview.html`、`frontend/tailwind.config.js`、`frontend/src/style.css` |
| 未改动 | 任何业务组件 `.vue`、路由、i18n、后端 —— 均未触碰 |

## 附录 A：对比度计算（可复现）

```python
def _lin(c):
    c /= 255.0
    return c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4

def lum(h):
    h = h.lstrip('#')
    r, g, b = (int(h[i:i + 2], 16) for i in (0, 2, 4))
    return 0.2126 * _lin(r) + 0.7152 * _lin(g) + 0.0722 * _lin(b)

def contrast(fg, bg):
    a, b = lum(fg), lum(bg)
    hi, lo = max(a, b), min(a, b)
    return round((hi + 0.05) / (lo + 0.05), 2)

# 例：主按钮白字 / 靛青 600
assert contrast('#FFFFFF', '#4F46E5') == 6.29
```

半透明叠色（焦点环、选中、徽章底）按 sRGB 通道线性混合后再计算，例如
`primary-500/60 over #FFFFFF = #A1A3F7 → 2.31:1`、`primary-400/70 over #070B14 = #5C65B4 → 3.73:1`。

## 附录 B：令牌速查（Tailwind 类 ↔ 语义）

```
页面底      bg-[var(--ui-canvas)]      暗 .dark 自动切换
卡片        .card / bg-white dark:bg-dark-800/60
主文字      text-accent-900 dark:text-dark-100
次文字      text-accent-600 dark:text-dark-300
三级文字    text-accent-600 dark:text-dark-400
主按钮      .btn-primary （primary-600→700 + 白字）
次按钮      .btn-secondary
链接        text-primary-600 dark:text-primary-400
边框        border-accent-200 dark:border-dark-700
强边框      border-accent-300 dark:border-dark-600
焦点环      focus-visible:ring-2 ring-primary-500 dark:ring-primary-400
选中        ::selection（style.css 令牌）
徽章        .badge-primary / -success / -warning / -danger / -gray
数字        tabular-nums（表默认） + font-mono（价格/密钥）
```