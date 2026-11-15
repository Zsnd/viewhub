---
name: ViewHub
description: 以现场画面为中心的紧凑中文视频工作台。
colors:
  primary: "#215d91"
  background: "#f7f8fa"
  surface: "#fff"
  text: "#202b39"
  muted: "#5d6877"
  border: "#e0e4e9"
  control: "#d4dae2"
  raised: "#f1f4f8"
  secondary: "#f1f3f5"
  secondary-text: "#273444"
  hover: "#eef3f8"
  hover-text: "#173b5d"
  ring: "#2363a0"
  sidebar: "#172b40"
  sidebar-selected: "#29465f"
  sidebar-text: "#b9c8d8"
  positive: "#1b7459"
  positive-surface: "#eaf5ef"
  negative: "#b4233b"
  screen: "#0d121a"
typography:
  headline:
    fontFamily: '"Public Sans Variable", "PingFang SC", "Microsoft YaHei UI", system-ui, sans-serif'
    fontSize: "24px"
    fontWeight: 650
    lineHeight: 1.4
    letterSpacing: "-0.025em"
  title:
    fontFamily: '"Public Sans Variable", "PingFang SC", "Microsoft YaHei UI", system-ui, sans-serif'
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.5
  body:
    fontFamily: '"Public Sans Variable", "PingFang SC", "Microsoft YaHei UI", system-ui, sans-serif'
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: '"Public Sans Variable", "PingFang SC", "Microsoft YaHei UI", system-ui, sans-serif'
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.5
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  xl: "12px"
  container: "10px"
spacing:
  tight: "4px"
  control-gap: "8px"
  compact: "12px"
  regular: "16px"
  panel: "20px"
  generous: "24px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.surface}"
    rounded: "{rounded.md}"
    height: "2.25rem"
    padding: "0.5rem 1rem"
  button-outline:
    backgroundColor: "{colors.background}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    height: "2.25rem"
    padding: "0.5rem 1rem"
  button-destructive:
    backgroundColor: "{colors.negative}"
    textColor: "{colors.surface}"
    rounded: "{rounded.md}"
    height: "2.25rem"
    padding: "0.5rem 1rem"
  button-secondary:
    backgroundColor: "{colors.secondary}"
    textColor: "{colors.secondary-text}"
    rounded: "{rounded.md}"
    height: "2.25rem"
    padding: "0.5rem 1rem"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    height: "2.25rem"
    padding: "0.5rem 1rem"
  input:
    backgroundColor: "transparent"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    height: "2.25rem"
    padding: "0.25rem 0.75rem"
  navigation:
    backgroundColor: "{colors.sidebar}"
    textColor: "{colors.sidebar-text}"
    padding: "26px 14px 16px"
  status-online:
    backgroundColor: "{colors.positive-surface}"
    textColor: "{colors.positive}"
    rounded: "9999px"
    padding: "0.125rem 0.5rem"
  card:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.container}"
    padding: "24px"
---

# Design System: ViewHub

## Overview

**Creative North Star: "现场监看工作台"**

使用用户选定的 shadcn/ui 标准组件与交互。墨蓝导航建立稳定边界，浅灰工作区承载白色工具面板，深色视频区域突出现场内容。中文界面紧凑、克制，状态和操作目标优先于装饰。

**Key Characteristics:**
- 深色导航、浅色管理、深色视频的功能分层。
- 紧凑工具栏、细边框、清晰的选中状态。
- 中文操作文案、独立状态文字、标准图标。

实现依据：`apps/platform/web/src/theme.css`、`apps/platform/web/src/viewhub.css`、`apps/platform/web/src/components/ui/` 及工作区、设备、接入、对讲、账号页面。前置方向中的通道栏宽度以已实现的 230px 为准。

## Colors

### Primary

沉稳操作蓝（`primary`）用于主要操作、时间轴定位和选中提示；`ring` 是 shadcn 焦点描边。`hover` 与 `hover-text` 用于轻量交互反馈。

### Neutral

墨蓝（`sidebar`）承载导航，`sidebar-selected` 标明当前页面；`sidebar-text` 是常态导航文字。浅灰（`background`）包围白色（`surface`）内容；`text` 和 `muted` 区分主次信息，`border` 与 `control` 区分容器边界和输入边界。`raised`、`secondary` 承载轻量层次。`screen` 专用于视频底色。

正向状态使用 `positive` 与 `positive-surface`；错误、危险确认和结束对讲使用 `negative`。状态同时有文字，不仅依赖颜色。

**The Semantic State Rule.** 设备在线、播放状态和能力支持分别表达，不把在线绿点解释成正在播放。

## Typography

界面统一使用 Public Sans Variable 与中文系统字体回退；无独立展示字体。根字号和行高见 `body`，数字采用等宽数字特性（`tabular-nums`）。

- 页面标题使用 `headline`；窄屏改为 22px。
- 区块标题使用 `title`；小节标题为 14px、600 字重。
- 普通文本使用 `body`；表单标签和设备表格正文通常为 12px。
- 辅助信息常为 10–12px；不将这些小字号用于页面主操作说明。
- shadcn 的 rem 字号以当前 14px 根字号计算：按钮 `text-sm` 为 0.875rem，输入默认 1rem、768px 起为 0.875rem。不要把 rem 控件尺寸误写成等值 px。

## Layout

桌面外壳是 208px 导航加弹性内容，顶栏高 64px，内容内边距 25px 28px。监控容器为 230px 通道栏加弹性画面，视频工作区内边距 20px。布局使用 `minmax(0,1fr)` 和可换行工具栏避免内容撑宽。

- 不宽于 1200px：外导航 180px、通道栏 200px；内容 22px 20px，画面内边距 16px；接入参数和账号表单为两列。
- 不宽于 900px：导航变为 220px 固定抽屉，遮罩覆盖正文，关闭时不可见；通道栏 190px。
- 不宽于 640px：顶栏 54px，内容 20px 14px；监控上下排列，通道目录可展开且最高 340px；参数与账号表单变单列，工具栏换行。设备表格保留横向滚动。
- 从 1100px 起，单画面使用 224px 右侧操作栏，集中放置云台与语音对讲；更窄时操作放在画面下方。

视频容器为直角；视频保持 16:9，宽度填满容器，使用 `object-fit: contain` 与居中的 `object-position`；普通单画面高度上限为 `calc(100dvh - 370px)`，最小高度桌面 200px、手机 160px。全屏取消该比例限制。监控墙用 1、4、9、16 个画面对应的网格列数。

## Elevation & Depth

常规内容依靠底色和细边框分层。选中的视图切换项有轻微阴影（`0 1px 3px #172b4012`）。shadcn 输入和描边按钮保留 `shadow-xs`，对话框使用 `shadow-lg` 和半透明黑色遮罩；不要用浮起卡片代替现有容器层次。抽屉遮罩使用 `#101b2a80`。

## Shapes

控件使用 `md`，视频使用直角，主要管理容器使用 `container`。shadcn 对话框使用 `lg`；原生 dialog 使用 `xl`，登录卡片为 14px。状态徽标通常是胶囊，画面标题旁徽标为 5px 圆角。细边框为 1px。图标沿用 Lucide 描边图标，常见尺寸 15–20px；不以字符替代图标。

## Components

### Buttons

主要操作使用实色操作蓝，次级操作使用描边、secondary 或 ghost；危险确认和结束对讲使用 destructive。默认高度与间距见前置 token；small 高 2rem，icon 为 2.25rem 方形。图标按钮必须有中文可访问名称。

primary 和 destructive 悬停使用对应色的 90% 不透明度，secondary 使用 80%，outline 和 ghost 使用轻蓝悬停底色。禁用时透明度 50%，shadcn 按钮不接收指针事件。保留焦点环与 `aria-invalid` 的错误描边。原生控件使用独立的 2px 焦点轮廓、3px 偏移。

### Inputs / Fields

标签置于字段上方，说明在字段下方；输入边界用 `control`。shadcn 输入有 3px、50% ring 色焦点环，错误使用 destructive 描边与 20% 环。只读编码使用独立的浅灰代码块。密码遮罩、显示和复制是有明确名称的独立操作。

### Navigation

当前页面由 `aria-current="page"` 与选中底色共同标识；普通项透明，悬停提亮。手机抽屉提供关闭遮罩、Escape、焦点约束及关闭后焦点恢复；展开时正文为 inert。视图切换、通道选择和接入详情切换使用 `aria-pressed`。

### Cards / Containers

主要容器使用白底细边框，设备详情内边距 24px，手机为 18px 14px；表格工具栏独立于行内容。表格悬停底色变化，密集列表通过分隔线组织，不为每行增加卡片阴影。

### Chips

在线状态使用正向浅底、正向文字及绿点，并附中文状态。未在线使用中性底色和文字。徽标不是可点击操作时，不增加虚假的悬停动作。

### Video / Control Dock

画面、通道标题和能力操作指向同一选中通道。连接提示置于视频左上；播放、暂停、停止、声音和全屏使用统一的播放器浮层图标栏，底部透明渐变保证可读性；正常播放闲置 2.5 秒渐隐，鼠标移动或键盘操作唤出，暂停、未播放、错误时常显；键盘焦点或鼠标停留于控件时不隐藏，触屏轻点画面切换显隐，减少动态效果设置下不做淡入淡出；停止使用实心直角方块，按钮保留可访问名称和提示，不使用表单按钮外观；系统全屏不可用时铺满浏览器窗口，Esc 退出；监听位于播放器浮层中，图标云台和对讲位于右侧操作区；实时和录像在当前通道内切换，回放时禁用现场控制；通道列表用文字与状态标记区分在线、离线、等待注册及注册失败，能力不足或离线时禁用或说明原因。空态提供说明和下一步，连接中与错误分别呈现；异步状态使用 `role="status"`，失败使用 `role="alert"`。

对话框使用 Radix 的焦点与键盘交互。危险操作写明对象及影响，失败保留对话框以便重试。保存、读取和连接中展示进行态并阻止重复提交。主场景切换不加装饰动效；原生控件颜色过渡为 0.12s，导航抽屉过渡为 0.2s。减少动态效果偏好把动画和过渡时长降至 0.01ms。

## Do's and Don'ts

### Do:
- **Do** 复用 shadcn/ui 标准交互及现有语义状态。
- **Do** 保留键盘焦点、中文可访问名称和文字状态反馈。
- **Do** 按现有断点换列、换行和折叠导航，保持视频目标与控制同步。

### Don't:
- **Don't** 将设备在线状态替代播放状态或能力信息。
- **Don't** 用字符图标、装饰动画或普遍卡片阴影改变现有工作台语言。
- **Don't** 用固定宽度阻断窄屏上的主要操作或对话框内容滚动。


### 播放器音量与图像调整

声音按钮的鼠标悬停与键盘 focus-within 显示上方音量浮层，使用 0–100 的原生范围控件和百分比读数，触屏可点按按钮后调节。播放器音量只作用于 HTML 视频/音频元素，改变数值不重新建立媒体连接、不修改系统音量。0% 使用静音图标，点击开启恢复上次非零音量。音量控件位于播放器活动范围内，拖动和键盘方向键不触发播放快捷键或触屏显隐。

通道操作区提供默认折叠的图像调整，明确仅调整当前显示。亮度、对比度、饱和度可单独调节，原始/提亮/通透为快捷预设。默认滤镜为 none；对比原图临时显示 none，还原恢复各值 100%。效果只作用于视频，不影响工具条、摄像头参数及原始媒体。当前为单画面临时设置，切换通道重新使用原始画面。

### 分屏与播放恢复

分屏视频无圆角、边框、内衬及黑色间隔；有画面直接呈现视频，未配置或离线使用纯黑背景。每格通道名称、状态和配置图标叠加于画面底部，复用单画面活动后显示、播放闲置渐隐、键盘聚焦保持显示的机制。视频保持比例，不裁切内容。工作区右上角全屏在单画面时仅铺满当前视频，在分屏时铺满整个监控墙；全屏内部提供退出入口，Escape 可退出，原生不可用时回退为窗口铺满。

点击在线通道直接播放，同一通道停止后再次点击也恢复播放。加载失败时，单画面和分屏都提供带刷新图标和文字的「重新播放」按钮。离线通道不尝试建立播放。

云台四方向支持鼠标、触屏和键盘按住，激活时显示按压状态；松开、取消、失焦、隐藏页面或切换通道停止。前端定时续期，服务端在续期中断后主动停止，保留原有云台归属与能力校验。停止和预置位继续为单次操作。
