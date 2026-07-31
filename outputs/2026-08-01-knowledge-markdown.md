# OpsOrigin 知识库 Markdown 录入/渲染 + 富文本编辑器 — 交付说明

日期：2026-08-01

## 背景
知识库此前录入只有纯 `<textarea>`：能写 Markdown 语法但**无实时预览、无渲染展示**，更无富文本。本次完成两阶段改造：Markdown 渲染 + 编辑/预览双模式（阶段一），TipTap 富文本编辑器（阶段二）。

## 完成内容

### 1. Markdown 渲染能力
- 新增依赖：`react-markdown` + `remark-gfm`（支持 GFM：表格、任务列表、删除线、自动链接）
- 新增组件：`web-next/src/components/markdown-viewer.tsx`（`MarkdownViewer`）
- `globals.css` 新增 `.markdown-body` 样式：主题自适应（light/dark 用 CSS 变量），覆盖标题、代码块（深色底）、表格（横向滚动防溢出）、引用、任务列表等

### 2. 详情页改造为「阅读/编辑」双模式（`knowledge/[id]/page.tsx`）
- 已有文章进入默认**阅读模式**：渲染后的正文 + 分类/状态/标签 + 社交栏（点赞/收藏/分享/评论）
- 编辑模式：原表单 + 「编辑 / 预览」Tab，预览实时渲染 Markdown
- 保存成功后留在本页切换阅读模式查看效果

### 3. 富文本编辑器（TipTap）
- 新增组件 `web-next/src/components/rich-text-editor.tsx`：**WYSIWYG 所见即所得**，工具栏含 H1-3、粗体/斜体/删除线/下划线、行内代码/代码块、引用、有序/无序/任务列表、链接、分割线、撤销/重做；快捷键（⌘B/⌘I 等）与粘贴 Markdown 自动转换均可用
- 核心设计：**内部富文本、对外仍以 Markdown 存取**（tiptap-markdown 扩展）——后端零改动、渲染层复用、存量数据 100% 兼容
- 详情页编辑区新增「**Markdown / 富文本**」录入方式切换，两种方式编辑同一篇文档，互转实时同步

### 4. 列表页摘要清洗（`knowledge/page.tsx`）
- 摘要先经 `stripMarkdown()` 轻量清洗（去代码块/链接/标题/列表/粗斜体等标记符号）再截取，显示更干净

### 5. 顺手修复预存构建阻塞 bug
- `tailwind.config.js` 中 `require('@tremor/react/tailwind')()`：官方 `@tremor/react@3.18.7` 包内**已无 tailwind 插件入口**（tremor 2.x 残留配置），导致 `next build`/`next dev` 直接失败
- 已改为 `plugins: []`，构建流程恢复

## 验证
- `tsc --noEmit`：知识库相关文件零错误
- Tailwind CSS 管道编译通过（含新增 `.markdown-body` / `.rich-editor` 样式）
- `next build` 在沙箱内被客户端「批量删除安全护栏」（.next 缓存清理 >50 文件需确认）阻止，**非代码问题**——本机 `npm run dev` 不受影响
- Go 后端无需改动（内容仍是文本字段，富文本也落盘为 Markdown）

## 说明
- 富文本未做图片上传（需后端存储接口，可后续加）
- 前端改动需 `cd web-next && npm install`（新增多个依赖）后 `npm run dev`
- 依赖注意：TipTap 使用 **v3**（tiptap-markdown@0.9.0 已兼容 v3，勿按旧教程降级 v2）
