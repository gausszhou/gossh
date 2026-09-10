# UI 样式优化指导(VS Code 风格)

> 来源:GoSSH 前端(`apps/web`,Vue 3 + xterm.js)一轮完整的界面样式重构实践。
> 目标:给另一个项目照抄这份"改造路线",把界面从"功能能用的拼凑风"收敛到
> **VS Code 默认配色与控件规格**的一致风格。
> 本文只讲**可复用的做法与真实取值**,不含业务逻辑。

---

## 0. 一句话方法论

> **把颜色、尺寸、控件形态全部收敛成 token;组件只引用 token,不再写字面量。**

改完之后,主题切换、整体调色、控件尺寸统一都变成"改一处"的事;风格漂移的根源
(每个组件各自定义按钮高度/圆角/边框色)被结构性消除。

---

## 1. 改造顺序(建议照此推进,每步可独立提交/回滚)

| 步骤 | 内容 | 产出 |
|---|---|---|
| 1 | **换 token 层**:色板 + 尺寸 token + 全局滚动条/焦点环 + 共享控件样式 | 全站立刻"看起来像 VS Code" |
| 2 | **左栏重构**:分组标题 + 列表行 + 行内操作 + 侧栏可调宽 | 信息密度与交互一致 |
| 3 | **顶部/内容区**:页签栏、工具条高度、终端面 | 骨架对齐 |
| 4 | **弹窗与残留**:共享控件落地、删掉重复定义与死代码 | 消除最后的不一致 |

关键点:**先做第 1 步再做组件**。反过来的话,每个组件都要改两遍。

---

## 2. Token 层(可直接抄的取值)

### 2.1 暗色(对齐 VS Code Dark Modern)

```css
:root {
    /* 面 */
    --bg-app: #1f1f1f;          /* 编辑器面(主内容背景) */
    --bg-bar: #181818;          /* 侧栏 / 页签栏 */
    --bg-bar-border: #2b2b2b;   /* 面板分隔线 */
    --bg-tab: #181818;          /* 非活动页签 */
    --bg-tab-active: #1f1f1f;   /* 活动页签 = 内容区同色 */
    --bg-tab-hover: #2a2d2e;    /* 列表/按钮悬停 */
    --bg-input: #313131;
    --bg-dialog: #252526;
    --border-dialog: #454545;
    --border-input: #3c3c3c;

    /* 文本(四级足够,别再多) */
    --fg: #cccccc;              /* 常规 */
    --fg-dim: #9d9d9d;          /* 次级 / 未选中列表项 */
    --fg-muted: #8b8b8b;        /* 分组标题 */
    --fg-bright: #ffffff;       /* 强调 / 活动项 */
    --fg-hint: #6e6e6e;         /* 占位符 / 弱提示 */

    /* 交互 */
    --accent: #0078d4;          /* 主按钮 / 活动页签顶条 */
    --accent-hover: #026ec1;
    --focus-border: #0078d4;    /* 输入框聚焦描边 */
    --list-active: #04395e;     /* 列表选中/操作按钮悬停底 */
    --overlay: rgba(0, 0, 0, 0.55);
    --shadow-dialog: 0 4px 16px rgba(0, 0, 0, 0.5);
    --shadow-widget: 0 2px 8px rgba(0, 0, 0, 0.36);

    /* 状态(统一一处,别在组件里写 #3fb950 之类) */
    --dot-idle: #8b8b8b;
    --dot-running: #3fb950;
    --dot-dead: #f85149;
    --net-good: #3fb950;
    --net-fair: #cca700;
    --net-bad: #f14c4c;

    /* 终端面:比编辑器面更深,页面容器与 xterm 主题必须同色 */
    --term-bg: #0c0c0c;

    /* 尺寸(单位统一 px,不用 rem 混搭) */
    --bar-height: 35px;         /* 页签栏 */
    --toolbar-height: 26px;     /* 次级工具条 */
    --row-height: 22px;         /* 单行列表项 */
    --radius-sm: 2px;           /* 输入框 / 小按钮 */
    --radius-md: 4px;           /* 图标按钮 / 菜单 */
    --radius-lg: 6px;           /* 对话框 */
}
```

### 2.2 亮色只覆盖 token,不碰组件

```css
[data-theme='light'] {
    --bg-app: #ffffff;
    --bg-bar: #f8f8f8;
    --bg-bar-border: #e5e5e5;
    --bg-tab-hover: #e8e8e8;
    --fg: #3b3b3b;
    --fg-dim: #616161;
    --fg-muted: #717171;
    --fg-bright: #000000;
    --fg-hint: #9d9d9d;
    --accent: #005fb8;
    --focus-border: #005fb8;
    --list-active: #e4e6f1;
    --overlay: rgba(0, 0, 0, 0.22);
    --term-bg: #ffffff;
}
```

**验收标准**:新主题只加一个 `[data-theme='x']` 块,不改任何组件文件。

### 2.3 全局共享控件(替代各组件重复定义)

```css
.btn-primary, .btn-secondary, .btn-ghost {
    display: inline-flex; align-items: center; justify-content: center;
    height: 26px; padding: 0 14px;
    border: 1px solid transparent; border-radius: var(--radius-sm);
    font-size: 13px; font-family: inherit; line-height: 1; cursor: pointer;
}
.btn-primary { background: var(--accent); color: #fff; }
.btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
.btn-secondary { background: var(--bg-tab-hover); border-color: var(--border-input); color: var(--fg); }
.btn-ghost { background: none; border-color: var(--border-input); color: var(--fg-dim); }

input[type='text'], input[type='password'], input[type='number'], select {
    background: var(--bg-input);
    border: 1px solid var(--border-input);
    border-radius: var(--radius-sm);
    color: var(--fg);
    outline: none;
}
input:focus, select:focus { border-color: var(--focus-border); }
```

> 注意 CSS 优先级:组件里若有**同名 scoped 类**(编译后带 `[data-v-xxx]`),
> 会覆盖全局定义。所以要**删除**组件里的重复定义,而不是指望全局覆盖它。

### 2.4 细滚动条(容易被忽略、收益很大)

```css
/* 面板容器:轨道透明 + 滑块半透明内缩,悬停加深 */
.panel ::-webkit-scrollbar { width: 10px; height: 10px; }
.panel ::-webkit-scrollbar-track { background: transparent; }
.panel ::-webkit-scrollbar-thumb {
    background: var(--scrollbar-thumb);
    border: 3px solid transparent;   /* 用透明边框实现内缩留白 */
    background-clip: content-box;
    border-radius: 5px;
}
.panel { scrollbar-width: thin; scrollbar-color: var(--scrollbar-thumb) transparent; }
```

### 2.5 焦点环 + 字体栈

```css
:focus-visible { outline: 1px solid var(--focus-border); outline-offset: -1px; }

body {
    font-family: 'Segoe UI', system-ui, -apple-system, BlinkMacSystemFont,
                 'Ubuntu', 'Droid Sans', sans-serif;
    font-size: 13px;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
}
/* 需要等宽的地方(地址/端口/路径/延迟)统一一个栈 */
.mono { font-family: 'SF Mono', Consolas, 'DejaVu Sans Mono', monospace; }
```

---

## 3. 组件级规格(数值可直接用)

### 3.1 骨架尺寸

| 元素 | 规格 |
|---|---|
| 页签栏 / 侧栏头部 | 35px 高,底边 1px `--bg-bar-border` |
| 次级工具条(会话工具条) | 26px 高 |
| 单行列表项 | 22px 高,内边距 `0 8px` |
| 两行列表项 | `padding: 4px 6px 4px 8px`,`gap: 1px`,约 39px |
| 图标按钮 | 20–24px 方形,`--radius-md` |
| 文本按钮 / 输入框 | 26px 高,`--radius-sm` |
| 对话框 | `--radius-lg` + `--shadow-dialog`,遮罩 `--overlay` |

### 3.2 页签(VS Code 关键观感)

```css
.tab {                     /* 非活动:与页签栏同色 */
    background: var(--bg-tab);
    border-right: 1px solid var(--bg-bar-border);
    color: var(--fg-dim);
}
.tab.active {
    background: var(--bg-tab-active);   /* = 内容区底色,视觉上"连通" */
    color: var(--fg-bright);
    position: relative;
}
.tab.active::before {                    /* 顶部 1px 强调条,替代整块高亮 */
    content: ''; position: absolute; inset: 0 0 auto 0;
    height: 1px; background: var(--accent);
}
.tab-close { opacity: 0; }               /* 关闭按钮只在悬停/活动时出现 */
.tab:hover .tab-close, .tab.active .tab-close { opacity: 1; }
```

> 常见错误:活动页签用**更深/更亮**的整块底色 + 圆角。VS Code 的做法是
> **同内容区底色 + 顶部细条**,这样页签与内容区连成一体。

### 3.3 列表项(资源管理器观感)

```css
.row {                         /* 两行信息 + 悬停出操作 */
    display: flex; flex-direction: column; gap: 1px;
    padding: 4px 6px 4px 8px;
    border-radius: var(--radius-sm);
    cursor: default;           /* 行本体不可点(见下) */
}
.row:hover { background: var(--bg-tab-hover); }

.row-line2 { position: relative; }
.row:hover .row-meta { visibility: hidden; }   /* 次要信息让位给按钮 */

.row-actions {                                  /* 操作按钮浮在第二行右侧 */
    position: absolute; right: 4px; bottom: 4px;
    display: none; align-items: center; gap: 1px;
}
.row:hover .row-actions,
.row:focus-within .row-actions { display: flex; }   /* 键盘可达 */
```

三条经验:

1. **危险操作不要用行本体触发**。行本体点击=主操作(如"连接")极易误触,
   改成**显式图标按钮**;行本体改为 `cursor: default`。
2. **主操作按钮用颜色区分**(如绿色播放),其余保持 `--fg-dim`,悬停才高亮。
3. **删除类操作保留两段式确认**(✓ / ✕),不要直接删。

### 3.4 右键状态菜单

- 用 `Teleport to="body"` 渲染,避免被侧栏 `overflow: hidden` 裁剪
- 定位:fixed + `Math.min(clientX, innerWidth - 菜单宽)` 做贴边内收
- 关闭:点击空白(`@mousedown.self`)+ `Esc`(全局 keydown),**在打开的下一帧再挂监听**,否则会被同一次事件立刻关掉
- 菜单项 24px 高、`--radius-sm`,悬停 `--list-active`;危险项用红色底

### 3.5 终端 / 内容面

- 容器与渲染器(themes/iframes/canvas)**背景色必须一致**,否则切换/重绘闪边
- 终端类内容加**约半个字符宽的内边距**(字号 14px 等宽 → `padding: 7px`);
  容器 `box-sizing: border-box` 时 `clientWidth/Height` 已扣掉 padding,
  自适应尺寸(FitAddon 之类)依然准确
- 工具条只放"当前上下文 + 状态",**不要塞功能入口**;功能入口应集中一处

### 3.6 空态

用**欢迎视图**而不是虚线卡片:

```css
.empty { display: flex; flex-direction: column; align-items: center; gap: 10px;
         text-align: center; max-width: 380px; }
.empty-icon  { color: var(--fg-hint); opacity: 0.55; }         /* 大图标,低透明度 */
.empty-title { font-size: 16px; font-weight: 300; color: var(--fg-bright); }  /* 细字重 */
.empty-hint  { font-size: 13px; color: var(--fg-hint); }
```

---

## 4. 三个高价值交互(建议都做)

### 4.1 侧栏可拖拽调宽

```ts
function startResize(e: PointerEvent) {
    const aside = (e.currentTarget as HTMLElement).parentElement!
    const left = aside.getBoundingClientRect().left       // 以起始左边界为基准
    resizing.value = true
    const onMove = (ev: PointerEvent) => {
        const next = Math.round(ev.clientX - left)
        width.value = Math.min(maxWidth(), Math.max(MIN, next))
    }
    const onUp = () => {
        resizing.value = false
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        localStorage.setItem(KEY, String(width.value))     // 只在松手时落盘
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    e.preventDefault()
}
```

```css
.resizer {
    position: absolute; top: 0; right: 0; width: 5px; height: 100%;
    cursor: col-resize; touch-action: none; background: transparent;
}
.resizer:hover, .resizer:active { background: var(--accent); }

/* 拖拽期间必须关掉过渡,否则指针跟随"粘手" */
.sidebar.resizing { transition: none; user-select: none; }
```

要点:宽度用**内联 style 覆盖**(折叠态不设内联宽度)→ `width: 0` 的折叠过渡不被内联值破坏;
双击分隔条恢复默认宽度;上限取 `min(560, 窗口宽 × 0.6)`。

### 4.2 折叠/展开入口的唯一性

**折叠后按钮不能跟着消失**。两种做法:

- 折中:按钮固定在不折叠的容器里(如页签栏最左侧),常驻
- 或:侧栏折叠为窄轨道(activity bar 式),按钮随轨道保留

若按钮放在被折叠的面板内,必须留一个"折叠态可见"的替代入口,否则用户无法恢复。

### 4.3 悬停揭示操作 + 键盘可达

`display: none → flex` 的悬停揭示要配 `:focus-within`,否则键盘用户永远看不到按钮。

---

## 5. 构建与验证

### 5.1 一个必须知道的坑:分块命名

若构建把入口与懒加载分块都固定命名为 `main.js`,打包器会把冲突的一方改名成 `main2.js`;
而"只把 `main.js` 拷进嵌入目录"的构建脚本会**静默丢掉分块**。

- 要么 `chunkFileNames: 'chunk-[hash].js'`
- 要么彻底不用懒加载(单入口直出),本文实践选了后者

### 5.2 脚本化的本地"改完即用"

样式改完要能看到效果,建议沉淀一个构建+安装脚本(`scripts/build-local.ps1` 为例):

1. 校验工具链(node/pnpm/go,含 `go.mod` 声明的版本下限)
2. 构建前端
3. 同步产物到后端嵌入目录(go:embed 之类)
4. 编译二进制并注入版本号(`git describe --tags --always --dirty`)
5. **结束正在运行的旧进程**(否则覆盖安装 exe 失败)
6. 安装 + 校验"内嵌产物确为本次构建"(比对产物片段)

> Windows 注意:含中文的 `.ps1` **必须保存为 UTF-8 with BOM**,否则 Windows
> PowerShell 5.1 会按系统 ANSI 代码页解析,中文串被拆坏 → 语法错误刷屏。

### 5.3 端到端验证(比人眼可靠)

用浏览器自动化(如 `agent-browser`)断言**结构与几何**,而不是靠截图肉眼看:

```
hover 行 → 断言 .row-actions 的 display 变 flex
click 主操作 → 断言新增了页签/会话(数量从 0→1)
量测 padding / backgroundColor → 断言等于 token 值(如 rgb(12,12,12))
折叠按钮 → 断言侧栏 width 在 0px 与设定值之间往返
拖拽分隔条 → 断言 width 变化 + localStorage 落盘;边界拖到底 → 断言被 clamp
console → 断言无 error
```

**踩过的坑**:悬停才显示的按钮在未悬停时 `getBoundingClientRect()` 全为 0,
按坐标点击会"点了个空"——要么先 `hover`,要么直接用选择器点击。

---

## 6. 落地检查清单

- [ ] 颜色/尺寸**全部**来自 token,组件内无 `#xxxxxx`、无魔法 px(除间距微调)
- [ ] 亮色主题只覆盖 token,文件 diff 不含组件
- [ ] 按钮/输入框在全局定义一处,组件里没有同名重复块
- [ ] 页签栏 35px、工具条 26px、列表行 22px(或 39px 两行),数值一致
- [ ] 活动页签 = 内容区底色 + 顶部 1px 强调条
- [ ] 滚动条为细样式(透明轨道 + 半透明滑块),悬停/滚动时可见
- [ ] `:focus-visible` 有 1px 焦点环;悬停揭示的操作支持键盘聚焦
- [ ] 主操作是显式按钮,行本体不触发破坏性/重量级操作
- [ ] 折叠入口在折叠态仍然可达
- [ ] 可调宽面板:拖拽禁用过渡 + 上限钳制 + 持久化 + 双击复位
- [ ] 终端/画布容器与渲染器背景同色,内边距约半字符
- [ ] 空态是欢迎视图(图标 + 细字重标题 + 副文案),不是虚线卡片
- [ ] 删除死代码:不再引用的组件、编译期开关、重复样式
- [ ] 端到端断言:结构、几何、交互、console 无错
- [ ] 提交按"token → 左栏 → 顶部/内容 → 弹窗与清理"分步,每步可独立回滚

---

## 7. 反模式(本项目改之前的状态,别重蹈)

| 反模式 | 后果 | 改法 |
|---|---|---|
| 组件各自定义按钮高度/圆角/边框色 | 全站 6 种按钮 | 收敛到全局共享类 |
| 活动页签整块高亮 + 圆角 | 与内容区割裂 | 同底色 + 顶部细条 |
| 行本体点击 = 连接/删除 | 误触 | 显式图标按钮 |
| 用 emoji 当图标(⚙ ✕ ⇄) | 跨平台字形/对齐不一致 | 统一图标库(lucide 等) |
| 写死的十六进制色散落各处 | 换主题要改几十处 | token 化 |
| 折叠按钮在被折叠面板内 | 折叠后无法展开 | 常驻位置或轨道保留 |
| 固定命名的懒加载分块 | 构建静默丢产物 | hash 命名或不做懒加载 |
| 只靠肉眼截图验收 | 回归无感知 | 结构与几何断言 |
