---
kind: frontend_style
name: 基于 Naive UI + CSS 设计令牌的前端样式体系
category: frontend_style
scope:
    - '**'
source_files:
    - frontend/src/styles/global.css
    - frontend/src/App.vue
    - frontend/src/components/DashboardLayout.vue
    - frontend/vite.config.ts
    - frontend/package.json
---

## 1. 使用的系统与工具
- 框架：Vue 3（Composition API + `<script setup>`）+ Vite 6，通过 `vue-tsc` 做类型检查。
- 组件库：**Naive UI**（`naive-ui@^2.40`），作为全局主题与交互组件的唯一来源。在 `App.vue` 中通过 `<n-config-provider>` 注入全局主题覆盖、中文语言包与日期本地化。
- 图标：`@vicons/ionicons5`，以函数式渲染方式传入 Naive UI 的 `icon` slot。
- 图表：ECharts 5（用于流量趋势等可视化）。
- 无 Tailwind / Sass / Less，纯原生 CSS 文件。

## 2. 关键文件
- `frontend/src/styles/global.css`：全局设计令牌（CSS Custom Properties）、基础排版、Markdown 渲染样式、通用页面/卡片网格工具类、响应式断点与动画。
- `frontend/src/App.vue`：Naive UI 根提供者，集中定义 `GlobalThemeOverrides`（主色、圆角等）。
- `frontend/src/components/DashboardLayout.vue`：布局壳（侧边栏 + 顶部栏 + 内容区），使用 Naive UI 的 `NMenu`、`NDrawer`、`NButton`、`NDropdown` 等构建管理后台骨架。
- `frontend/vite.config.ts`：仅配置 Vue 插件、`@` 路径别名、开发代理与 `dist` 输出目录，不引入任何 CSS 预处理或 PostCSS 插件。
- `frontend/package.json`：声明依赖，确认未引入任何 CSS-in-JS 或原子化 CSS 库。

## 3. 架构与约定
### 设计令牌（Design Tokens）
所有视觉变量集中在 `global.css` 的 `:root` 伪类中，包括：
- 背景/文字/边框色：`--bg`、`--bg-soft`、`--card`、`--border`、`--text`、`--text-2`、`--text-3`
- 强调色：`--accent`、`--accent-strong`、`--accent-soft`
- 语义色：`--danger`、`--warn`、`--info` 及其 soft 变体
- 阴影：`--shadow`、`--shadow-sm`
- 圆角：`--r`（16px）、`--r-sm`（10px）
- 字体栈：`--ff`，优先 Inter / SF Pro / system-ui / 微软雅黑 / 苹方
这些令牌被全局 body、链接、Markdown 渲染块以及各组件的 `scoped` 样式引用，保证全站点色彩一致。

### 主题系统
- **Naive UI 主题**：在 `App.vue` 中以 `theme-overrides` 形式覆盖 `primaryColor`、`primaryColorHover`、`primaryColorPressed`、`primaryColorSuppl` 和全局 `borderRadius`，使按钮、菜单、输入框等控件统一为深灰主色 + 10px 圆角的风格。
- **CSS 自定义属性**：除 Naive UI 内置组件外，业务组件全部通过 `var(--*)` 引用令牌，避免硬编码颜色。
- **语言与日期**：通过 `zhCN`、`dateZhCN` 将 Naive UI 提示文案与日期格式切换为中文。

### 布局与响应式
- 桌面端：`DashboardLayout` 提供固定宽度（220px）左侧导航 + 右侧内容区；移动端通过 `window.matchMedia('(max-width: 768px)')` 切换为抽屉式导航。
- 栅格：`card-grid` 使用 `grid-template-columns: repeat(auto-fill, minmax(300px, 1fr))`，在 ≤640px 时退化为单列；统计行 `stat-row` 用 `auto-fit` 自适应。
- 断点：768px（移动端标题/间距调整）、640px（卡片网格单列）。

### Markdown 渲染样式
`global.css` 内嵌 `.md` 命名空间下的完整 Markdown 渲染样式（标题层级、代码块、引用、表格斑马纹、图片圆角等），供帮助文档、公告等富文本展示复用。

### 样式组织原则
- 全局层：`styles/global.css` 只放设计令牌、reset、通用工具类和 Markdown 样式。
- 组件层：每个 `.vue` 组件使用 `<style scoped>` 写局部样式，并通过 `:deep()` 穿透 Naive UI 内部 DOM（如菜单分组标签、选择器选项高度）。
- 无独立主题文件 / 无多套主题切换逻辑，当前为单一“浅灰底 + 深灰强调”的亮色主题。

## 4. 约定与约束
- **禁止直接硬编码颜色值**：业务组件应通过 `var(--text)`、`var(--bg-soft)` 等令牌获取颜色，而非写入十六进制字面量（可在 `global.css` 中看到这一模式被广泛遵循）。
- **Naive UI 组件必须经 `<n-config-provider>` 包裹**：全局主题、语言、消息/对话框 Provider 均在 `App.vue` 顶层注入，子组件直接使用 `N*` 组件即可继承。
- **组件级样式使用 scoped**：布局与页面样式写在各自组件的 `<style scoped>` 中，并通过 `:deep()` 精准覆盖 Naive UI 内部类名（例如 `.n-menu-item-content`、`.n-base-select-option`）。
- **长选项下拉需配合宽菜单类**：当 Naive UI 的 `n-select` / `n-popselect` 选项过长时，需添加 `qz-wide-menu` 类并关闭虚拟滚动（注释明确说明原因与用法）。
- **响应式策略**：以 CSS Grid + `@media (max-width: 640px|768px)` 为主，JS 仅用于检测移动端以切换侧边栏/抽屉形态，不在 JS 中计算样式。
- **构建产物**：Vite 将前端打包到 `frontend/dist`，由 Go 后端通过 `embed.go` 嵌入二进制，因此样式最终随面板一起发布，不存在运行时动态加载外部 CSS 的行为。