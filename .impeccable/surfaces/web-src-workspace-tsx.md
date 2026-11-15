---
version: 1
slug: "web-src-workspace-tsx"
primary_target: "apps/platform/web/src/workspace.tsx"
related_targets: ["apps/platform/web/src/device-settings.tsx","apps/platform/web/src/viewhub.css"]
---

# 实时监控工作区

Mode: Operate. 用户已确认日常监看优先，兼顾设备管理。品牌与组件方向由用户指定为 shadcn/ui，直接在代码中实现。

## Direction contract

THESIS: 以当前选中的现场画面为工作中心；实时、录像回放、单画面和分屏在同一工作区，设备接入独立成维护任务。

OWN-WORLD: shadcn/ui 标准交互，墨蓝导航、浅灰管理工作区、深色视频区域；公共字体和克制的状态色，统一紧凑工具栏。

STORY: 进入即看到通道，选择现场，播放、查看录像或操作；注册失败时进入设备详情定位并恢复。

FIRST VIEWPORT: 左侧 208px 主导航，监控页内 230px 可搜索通道目录，其余为视频工作区；顶端为页面标题和视图切换，选中通道的图标云台与语音对讲统一放在 224px 右侧操作区，小屏移至画面下方；通道状态以文字和标记共同显示。选中画面和操作目标同步变化是标志性交互。

FORM: 用户指定 shadcn/ui，采用标准后台导航与视频工作区；seed: none (brief-pinned)。抽屉与对话框仅用于明确的次级任务。主场景切换不使用装饰性动效，选中状态即时响应。

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
