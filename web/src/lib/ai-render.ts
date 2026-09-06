// ============================================================================
// AI 消息渲染共享模块
// 供 /ai 完整页与全局悬浮助手共用, 保证两处渲染行为一致。
// ============================================================================

export function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** 去除 <answer>...</answer> 包装(保留内部正文): 部分推理模型会把最终答案包在里面,
 *  否则 Markdown 渲染器会把它当原始文本显示成 "<answer>…"。 */
export function stripAnswerWrap(text: string): string {
  if (!text) return text
  return text
    .replace(/<answer[^>]*>([\s\S]*?)<\/answer>/gi, '$1')
    .replace(/<\/?answer>/gi, '')
}

/** 渲染前全量清洗模型噪声标签: 去 <answer> 包装 + 去未闭合/残留的 <think:6124c78e> 残片。
 *  用于标准 Markdown 路径(renderMarkdown); 自主模式路径用 stripAnswerWrap 以保留 <think> 折叠块。 */
export function stripModelWrappers(text: string): string {
  if (!text) return text
  return stripAnswerWrap(text)
    .replace(/<think[^>]*>([\s\S]*?)<\/think>/gi, '')
    .replace(/<\/?think>/gi, '')
}

export function renderInlineMd(s: string): string {
  let h = escapeHtml(s)
  h = h.replace(/`([^`]+)`/g, '<code class="rounded bg-gray-200 dark:bg-gray-700 px-1 text-xs">$1</code>')
  h = h.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  h = h.replace(/\n/g, '<br/>')
  return h
}

// 行级 Markdown→HTML: 先按行扫描识别块级元素(代码/标题/列表/引用/分隔线),
// 再对每段执行内联替换(粗体/斜体/链接/行内代码/引用上标)。
// 设计目标: 覆盖 AI 答复里高频出现的格式(# / ## / ### / 有序列表 / 无序列表 / > 引用 / ---),
// 表格/任务列表等 GFM 扩展暂不内置(如有需要可替换为 marked/markdown-it)。
export function renderMarkdown(text: string): string {
  if (!text) return ''
  // 0) 清洗模型噪声标签(<answer> 包装 / 残留 <think:6124c78e> 残片), 避免原样显示
  text = stripModelWrappers(text)
  // 1) 保护代码块 ```lang\n...\n``` (跨行)
  const codeBlocks: string[] = []
  let html = text.replace(/```(\w*)\n([\s\S]*?)```/g, (_m, lang, code) => {
    codeBlocks.push(code)
    const langCls = lang ? ` data-lang="${lang}"` : ''
    return `\u0000CODE${codeBlocks.length - 1}\u0000${langCls}`
  })

  // 2) 按行处理块级
  const lines = html.split('\n')
  const out: string[] = []
  let listType: 'ul' | 'ol' | null = null
  let blockquoteBuffer: string[] = []

  const closeList = () => {
    if (listType) {
      out.push(`</${listType}>`)
      listType = null
    }
  }
  const flushBlockquote = () => {
    if (blockquoteBuffer.length) {
      const joined = blockquoteBuffer.join(' ').trim()
      if (joined) out.push(`<blockquote class="border-l-4 border-gray-300 dark:border-gray-600 pl-3 my-2 text-gray-700 dark:text-gray-300">${renderInline(joined)}</blockquote>`)
      blockquoteBuffer = []
    }
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trim()

    // 空行: 关闭所有块级
    if (!trimmed) {
      closeList()
      flushBlockquote()
      continue
    }

    // 标题: 跳过序数前缀(最多 3 个 #)
    const heading = trimmed.match(/^(#{1,3})\s+(.+)$/)
    if (heading) {
      closeList()
      flushBlockquote()
      const level = heading[1].length
      const sizeCls = level === 1 ? 'text-lg font-semibold mt-3 mb-1.5' : level === 2 ? 'text-base font-semibold mt-2.5 mb-1.5' : 'text-sm font-semibold mt-2 mb-1'
      out.push(`<div class="${sizeCls}">${renderInline(heading[2])}</div>`)
      continue
    }

    // 引用
    if (trimmed.startsWith('>')) {
      closeList()
      blockquoteBuffer.push(trimmed.replace(/^>\s?/, ''))
      continue
    } else {
      flushBlockquote()
    }

    // 分隔线
    if (/^-{3,}$|^\*{3,}$|^_{3,}$/.test(trimmed)) {
      closeList()
      out.push('<hr class="my-2 border-gray-200 dark:border-gray-700" />')
      continue
    }

    // 有序列表
    const olMatch = trimmed.match(/^(\d+)[.\u3001)]\s+(.+)$/)
    if (olMatch) {
      if (listType !== 'ol') {
        closeList()
        out.push('<ol class="list-decimal list-inside space-y-1 my-1.5 pl-1">')
        listType = 'ol'
      }
      out.push(`<li class="leading-relaxed">${renderInline(olMatch[2])}</li>`)
      continue
    }

    // 无序列表
    const ulMatch = trimmed.match(/^[-*+]\s+(.+)$/)
    if (ulMatch) {
      if (listType !== 'ul') {
        closeList()
        out.push('<ul class="list-disc list-inside space-y-1 my-1.5 pl-1">')
        listType = 'ul'
      }
      out.push(`<li class="leading-relaxed">${renderInline(ulMatch[1])}</li>`)
      continue
    }

    // 表格: 连续以 | 开头的行(GFM)。首行表头, 第二行分隔(|---|---|), 其余数据行。
    if (
      trimmed.startsWith('|') && i + 1 < lines.length &&
      lines[i + 1].trim().startsWith('|') && /^[\s|:-]+$/.test(lines[i + 1].trim())
    ) {
      closeList()
      flushBlockquote()
      const tblLines: string[] = []
      while (i < lines.length && lines[i].trim().startsWith('|')) {
        tblLines.push(lines[i].trim())
        i++
      }
      i-- // 抵消 for 循环的 i++
      const parseRow = (row: string) => row.replace(/^\|/, '').replace(/\|$/, '').split('|').map(c => c.trim())
      const head = parseRow(tblLines[0] || '')
      const bodyRows = tblLines.slice(2).map(parseRow) // 第 2 行是分隔线, 跳过
      const th = head.map(c => `<th class="border border-gray-200 dark:border-gray-700 bg-gray-100 dark:bg-gray-800 px-2 py-1.5 text-left font-semibold whitespace-nowrap">${renderInline(c)}</th>`).join('')
      const trs = bodyRows.map(r => `<tr>${r.map(c => `<td class="border border-gray-200 dark:border-gray-700 px-2 py-1 align-top">${renderInline(c)}</td>`).join('')}</tr>`).join('')
      out.push(`<div class="my-2 overflow-x-auto"><table class="w-full text-xs border-collapse"><thead><tr>${th}</tr></thead><tbody>${trs}</tbody></table></div>`)
      continue
    }

    // 普通段落
    closeList()
    out.push(`<p class="leading-relaxed my-1.5">${renderInline(line)}</p>`)
  }
  closeList()
  flushBlockquote()

  html = out.join('')

  // 3) 还原代码块
  html = html.replace(/\u0000CODE(\d+)\u0000(?:\s*data-lang="(\w+)")?/g, (_m, i, lang) => {
    const label = lang ? `<span class="text-[9px] uppercase tracking-wide text-gray-500 mb-1 block">${lang}</span>` : ''
    return `<pre class="rounded bg-gray-900 text-gray-100 p-3 my-2 overflow-x-auto text-xs">${label}<code>${escapeHtml(codeBlocks[+i])}</code></pre>`
  })

  return html
}

// 内联 Markdown→HTML(必须在块级切分后调用): 行内代码/粗体/斜体/链接/图片/引用上标/换行占位
function renderInline(s: string): string {
  let h = escapeHtml(s)
  // 图片语法 ![alt](url) 必须在普通链接之前: 否则 alt/URL 会被当普通链接吃掉。
  // URL 必须以 http/https/data:image/blob 开头或绝对路径(防止注入 javascript: / file: 等危险协议)。
  h = h.replace(/!\[([^\]]*)\]\(((?:https?:\/\/|data:image\/|\/)[^)\s]+)\)/g,
    (_m, alt, url) => `<img class="my-1.5 max-w-full rounded border border-gray-200 dark:border-gray-700" src="${url}" alt="${alt}" loading="lazy" referrerpolicy="no-referrer" />`)
  // 行内代码需先于粗体,避免内部 ** 误匹配
  h = h.replace(/`([^`]+)`/g, '<code class="rounded bg-gray-200 dark:bg-gray-700 px-1 text-xs">$1</code>')
  h = h.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  h = h.replace(/\*([^*\n]+?)\*/g, '<em>$1</em>')
  h = h.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a class="text-blue-500 underline" href="$2" target="_blank" rel="noopener">$1</a>')
  h = h.replace(/\[\^([a-zA-Z0-9_-]+)\]/g, '<sup class="text-blue-500 font-semibold text-[10px] select-none" id="cite-ref-$1">[$1]</sup>')
  return h
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
    <ol class="space-y-1.5">${lines.map((l, i) => `<li class="flex gap-2 text-xs"><span class="shrink-0 h-4 w-4 rounded-full bg-blue-500 text-white text-[9px] flex items-center justify-center mt-px font-semibold">${i + 1}</span><span class="text-gray-700 dark:text-[var(--foreground)]">${renderInlineMd(l)}</span></li>`).join('')}</ol>
  </div>`
}

// 渲染 <report> 块: 最终报告卡片
export function renderReportBlock(content: string): string {
  return `<div class="mt-2 rounded-lg border border-purple-200 dark:border-purple-500/30 bg-purple-50/60 dark:bg-purple-500/5 p-3">
    <div class="flex items-center gap-1.5 mb-2">
      <span class="text-[10px] font-semibold uppercase tracking-wide text-purple-600 dark:text-purple-400">📄 执行报告</span>
    </div>
    <div class="text-xs text-gray-700 dark:text-[var(--foreground)] leading-relaxed">${renderInlineMd(content)}</div>
  </div>`
}

// 自主模式结构化渲染:
// 将 AI 流式输出中的 <plan>/<step>/<report>/<think> 标签与工具调用标记解析为卡片式展示。
export function renderExploreContent(text: string): string {
  let html = stripAnswerWrap(text)
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
