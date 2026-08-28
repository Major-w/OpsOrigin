// ============================================================================
// AI 消息渲染共享模块
// 供 /ai 完整页与全局悬浮助手共用, 保证两处渲染行为一致。
// ============================================================================

export function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

export function renderInlineMd(s: string): string {
  let h = escapeHtml(s)
  h = h.replace(/`([^`]+)`/g, '<code class="rounded bg-gray-200 dark:bg-gray-700 px-1 text-xs">$1</code>')
  h = h.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  h = h.replace(/\n/g, '<br/>')
  return h
}

// 轻量内联 Markdown→HTML。生产中可替换为 react-markdown+remark-gfm 获得完整 GFM 支持。
export function renderMarkdown(text: string): string {
  let html = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, '<pre class="rounded bg-gray-900 text-gray-100 p-3 my-2 overflow-x-auto text-xs"><code>$2</code></pre>')
  html = html.replace(/`([^`]+)`/g, '<code class="rounded bg-gray-200 dark:bg-gray-700 px-1 text-xs">$1</code>')
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  html = html.replace(/\*([^*\n]+?)\*/g, '<em>$1</em>')
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a class="text-blue-500 underline" href="$2" target="_blank">$1</a>')
  // RAG 引用标注 [^id] → 上标锚点
  html = html.replace(/\[\^([a-zA-Z0-9_-]+)\]/g, '<sup class="text-blue-500 font-semibold text-[10px] select-none" id="cite-ref-$1">[$1]</sup>')
  html = html.replace(/\n/g, '<br/>')
  return html
}

// 渲染 <plan> 块: 步骤列表卡片
export function renderPlanBlock(content: string): string {
  const lines = content
    .split('\n')
    .map(l => l.trim())
    .filter(Boolean)
    .map(l => l.replace(/^\d+[.、)]\s*/, ''))
  return `<div class="mt-2 rounded-lg border border-blue-200 dark:border-blue-500/30 bg-blue-50/60 dark:bg-blue-500/5 p-3">
    <div class="flex items-center gap-1.5 mb-2">
      <span class="text-[10px] font-semibold uppercase tracking-wide text-blue-600 dark:text-blue-400">📋 执行计划</span>
      <span class="text-[10px] text-muted-foreground">(${lines.length} 步)</span>
    </div>
    <ol class="space-y-1.5">${lines.map((l, i) => `<li class="flex gap-2 text-xs"><span class="shrink-0 h-4 w-4 rounded-full bg-blue-500 text-white text-[9px] flex items-center justify-center mt-px font-semibold">${i + 1}</span><span class="text-gray-700 dark:text-[#C7D2E0]">${renderInlineMd(l)}</span></li>`).join('')}</ol>
  </div>`
}

// 渲染 <report> 块: 最终报告卡片
export function renderReportBlock(content: string): string {
  return `<div class="mt-2 rounded-lg border border-purple-200 dark:border-purple-500/30 bg-purple-50/60 dark:bg-purple-500/5 p-3">
    <div class="flex items-center gap-1.5 mb-2">
      <span class="text-[10px] font-semibold uppercase tracking-wide text-purple-600 dark:text-purple-400">📄 执行报告</span>
    </div>
    <div class="text-xs text-gray-700 dark:text-[#C7D2E0] leading-relaxed">${renderInlineMd(content)}</div>
  </div>`
}

// 自主模式结构化渲染:
// 将 AI 流式输出中的 <plan>/<step>/<report>/<think> 标签与工具调用标记解析为卡片式展示。
export function renderExploreContent(text: string): string {
  let html = text
  // 1. 保护代码块
  const codeBlocks: string[] = []
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_m, _lang, code) => {
    codeBlocks.push(code)
    return `\u0000CODE${codeBlocks.length - 1}\u0000`
  })
  // 2. 提取 <think> 推理过程 → 可折叠
  const thinks: string[] = []
  html = html.replace(/<think>([\s\S]*?)<\/think>/gi, (_m, p) => {
    thinks.push(p)
    return `\u0000THINK${thinks.length - 1}\u0000`
  })
  // 3. 提取 <plan>
  const plans: string[] = []
  html = html.replace(/<plan>([\s\S]*?)<\/plan>/gi, (_m, p) => {
    plans.push(p)
    return `\u0000PLAN${plans.length - 1}\u0000`
  })
  // 4. 提取 <report>
  const reports: string[] = []
  html = html.replace(/<report>([\s\S]*?)<\/report>/gi, (_m, p) => {
    reports.push(p)
    return `\u0000REPORT${reports.length - 1}\u0000`
  })
  // 5. <step> 高亮为步骤徽章
  html = html.replace(/<step>([\s\S]*?)<\/step>/gi, '<span class="inline-flex items-center gap-1 text-[11px] font-medium text-blue-700 dark:text-blue-400 bg-blue-100 dark:bg-blue-500/15 rounded px-1.5 py-0.5 my-0.5">▶ $1</span>')
  // 6. 工具调用标记 → 可折叠面板
  html = html.replace(/\n*🔧 \*\*调用工具 `([^`]+)`\*\*/g, '<details class="my-1.5" open><summary class="text-[11px] text-amber-600 dark:text-amber-400 cursor-pointer hover:underline">🔧 工具调用: <code class="font-mono">$1</code></summary>')
  html = html.replace(/\n*↳ \*\*结果\*\*: /g, '<div class="mt-1 text-[11px] text-muted-foreground">↳ 结果: ')
  // 7. 转义剩余原始文本
  html = html.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  // 8. 还原占位块
  html = html.replace(/\u0000CODE(\d+)\u0000/g, (_m, i) => `<pre class="rounded bg-gray-900 text-gray-100 p-3 my-2 overflow-x-auto text-xs"><code>${escapeHtml(codeBlocks[+i])}</code></pre>`)
  html = html.replace(/\u0000THINK(\d+)\u0000/g, (_m, i) => `<details class="mt-1 mb-2 rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.06)] bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] p-2"><summary class="text-[11px] text-muted-foreground cursor-pointer hover:text-foreground">💭 推理过程</summary><div class="mt-1.5 text-[11px] text-muted-foreground whitespace-pre-wrap leading-relaxed">${escapeHtml(thinks[+i])}</div></details>`)
  html = html.replace(/\u0000PLAN(\d+)\u0000/g, (_m, i) => renderPlanBlock(plans[+i]))
  html = html.replace(/\u0000REPORT(\d+)\u0000/g, (_m, i) => renderReportBlock(reports[+i]))
  // 9. 通用 markdown: 行内代码 / 加粗 / 换行
  html = html.replace(/`([^`]+)`/g, '<code class="rounded bg-gray-200 dark:bg-gray-700 px-1 text-xs">$1</code>')
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  html = html.replace(/\n/g, '<br/>')
  return html
}
