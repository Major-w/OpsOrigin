// ============================================================================
// 轻量语法高亮(零依赖): 单遍分词器, 按语言配置生成 <span class="tok-*">。
// 设计目标: AI 助手产出的运维类代码(shell/yaml/json/python/go/sql/js)可读性与
// 观感对齐主流对话产品(Claude/ChatGPT), 且不引入 highlight.js 等重依赖 /
// 不触发 npm 网络安装(沙箱受限)。
//
// 关键约束:
//  - 调用方须传入「未转义」的原始代码(highlightCode 内部统一转义), 否则会双重转义。
//  - 采用 sticky(y) 正则 + 有序 alternation, 每次至少推进 1 字符, 不会死循环,
//    也不会出现「字符串内误匹配关键字」的经典正则高亮 bug(字符串/注释优先于关键字)。
// ============================================================================

export type TokenClass =
  | 'tok-c' // comment
  | 'tok-s' // string
  | 'tok-n' // number
  | 'tok-k' // keyword
  | 'tok-b' // boolean / builtin
  | 'tok-f' // function call
  | 'tok-p' // object/yml key (property)
  | 'tok-o' // operator / punctuation

interface TokenDef {
  name: string
  cls?: TokenClass
  src: string
}

// 公共片段(所有语言复用)
const STR = String.raw`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|` + '`(?:[^`\\\\]|\\\\.)*`'
const NUM = String.raw`\b0x[0-9a-fA-F]+\b|\b\d[\d_]*(?:\.\d+)?\b`
const BOOL = String.raw`\b(?:true|false|null|nil|None|True|False|Undefined|undefined|NaN)\b`
const FN = String.raw`[A-Za-z_$][\w$]*(?=\s*\()`
const IDENT = String.raw`[A-Za-z_$][\w$]*`
const OP = String.raw`[(){}\[\];,.:=<>+*/%&|!?@~^]+`
const WS = String.raw`\s+`
const ANY = String.raw`[\s\S]`

const kw = (words: string) => String.raw`\b(?:` + words + String.raw`)\b`

// 各语言配置: 顺序即 alternation 优先级(越靠前越先匹配)。
const RULES: Record<string, TokenDef[]> = {
  bash: [
    { name: 'comment', cls: 'tok-c', src: String.raw`#.*` },
    { name: 'str', cls: 'tok-s', src: STR },
    { name: 'num', cls: 'tok-n', src: NUM },
    { name: 'bool', cls: 'tok-b', src: BOOL },
    { name: 'kw', cls: 'tok-k', src: kw('if|then|elif|else|fi|for|while|until|do|done|case|esac|in|function|return|exit|export|local|readonly|source|select') },
    { name: 'fn', cls: 'tok-f', src: FN },
    { name: 'ident', src: IDENT },
    { name: 'op', cls: 'tok-o', src: OP },
    { name: 'ws', src: WS },
    { name: 'any', src: ANY },
  ],
  json: [
    { name: 'comment', cls: 'tok-c', src: String.raw`//.*|/\*[\s\S]*?\*/` },
    { name: 'prop', cls: 'tok-p', src: String.raw`"(?:[^"\\]|\\.)*"(?=\s*:)` },
    { name: 'str', cls: 'tok-s', src: STR },
    { name: 'num', cls: 'tok-n', src: NUM },
    { name: 'bool', cls: 'tok-b', src: BOOL },
    { name: 'ident', src: IDENT },
    { name: 'op', cls: 'tok-o', src: OP },
    { name: 'ws', src: WS },
    { name: 'any', src: ANY },
  ],
  yaml: [
    { name: 'comment', cls: 'tok-c', src: String.raw`#.*` },
    // prop 须位于行首或空白之后(否则 url 值里的 x.com: 会被误判成键), 且冒号后不是 //
    { name: 'prop', cls: 'tok-p', src: String.raw`(?<=^|\s)[A-Za-z_][\w.\-]*(?=\s*:(?!\/))` },
    { name: 'str', cls: 'tok-s', src: STR },
    { name: 'num', cls: 'tok-n', src: NUM },
    { name: 'bool', cls: 'tok-b', src: BOOL },
    { name: 'kw', cls: 'tok-k', src: kw('true|false|null|yes|no|on|off|True|False|Null|Yes|No|On|Off') },
    { name: 'fn', cls: 'tok-f', src: FN },
    { name: 'ident', src: IDENT },
    { name: 'op', cls: 'tok-o', src: OP },
    { name: 'ws', src: WS },
    { name: 'any', src: ANY },
  ],
  python: [
    { name: 'comment', cls: 'tok-c', src: String.raw`#.*` },
    { name: 'str', cls: 'tok-s', src: STR },
    { name: 'num', cls: 'tok-n', src: NUM },
    { name: 'bool', cls: 'tok-b', src: BOOL },
    { name: 'kw', cls: 'tok-k', src: kw('def|class|return|if|elif|else|for|while|in|is|not|and|or|import|from|as|with|try|except|finally|raise|lambda|yield|async|await|pass|break|continue|global|nonlocal|assert|del|None|True|False|self') },
    { name: 'fn', cls: 'tok-f', src: FN },
    { name: 'ident', src: IDENT },
    { name: 'op', cls: 'tok-o', src: OP },
    { name: 'ws', src: WS },
    { name: 'any', src: ANY },
  ],
  go: [
    { name: 'comment', cls: 'tok-c', src: String.raw`//.*|/\*[\s\S]*?\*/` },
    { name: 'str', cls: 'tok-s', src: STR },
    { name: 'num', cls: 'tok-n', src: NUM },
    { name: 'bool', cls: 'tok-b', src: BOOL },
    { name: 'kw', cls: 'tok-k', src: kw('func|package|import|var|const|type|struct|interface|map|chan|go|defer|return|if|else|for|range|switch|case|default|break|continue|fallthrough|select|goto') },
    { name: 'fn', cls: 'tok-f', src: FN },
    { name: 'ident', src: IDENT },
    { name: 'op', cls: 'tok-o', src: OP },
    { name: 'ws', src: WS },
    { name: 'any', src: ANY },
  ],
  sql: [
    { name: 'comment', cls: 'tok-c', src: String.raw`--.*|/\*[\s\S]*?\*/` },
    { name: 'str', cls: 'tok-s', src: STR },
    { name: 'num', cls: 'tok-n', src: NUM },
    { name: 'kw', cls: 'tok-k', src: kw('select|from|where|insert|into|values|update|set|delete|create|table|index|view|drop|alter|join|left|right|inner|outer|on|group|by|order|having|limit|offset|as|and|or|not|null|is|in|exists|between|like|distinct|count|sum|avg|min|max|case|when|then|else|end|primary|key|foreign|references|default|constraint|union|all|with') },
    { name: 'fn', cls: 'tok-f', src: FN },
    { name: 'ident', src: IDENT },
    { name: 'op', cls: 'tok-o', src: OP },
    { name: 'ws', src: WS },
    { name: 'any', src: ANY },
  ],
  js: [
    { name: 'comment', cls: 'tok-c', src: String.raw`//.*|/\*[\s\S]*?\*/` },
    { name: 'str', cls: 'tok-s', src: STR },
    { name: 'num', cls: 'tok-n', src: NUM },
    { name: 'bool', cls: 'tok-b', src: BOOL },
    { name: 'kw', cls: 'tok-k', src: kw('const|let|var|function|return|if|else|for|while|do|switch|case|default|break|continue|new|class|extends|super|this|import|export|from|as|async|await|try|catch|finally|throw|typeof|instanceof|in|of|yield|void|delete') },
    { name: 'fn', cls: 'tok-f', src: FN },
    { name: 'ident', src: IDENT },
    { name: 'op', cls: 'tok-o', src: OP },
    { name: 'ws', src: WS },
    { name: 'any', src: ANY },
  ],
}

// 未知语言: 通用兜底(注释/字符串/数字/布尔)
const DEFAULT: TokenDef[] = [
  { name: 'comment', cls: 'tok-c', src: String.raw`#.*|//.*|/\*[\s\S]*?\*/` },
  { name: 'str', cls: 'tok-s', src: STR },
  { name: 'num', cls: 'tok-n', src: NUM },
  { name: 'bool', cls: 'tok-b', src: BOOL },
  { name: 'ident', src: IDENT },
  { name: 'op', cls: 'tok-o', src: OP },
  { name: 'ws', src: WS },
  { name: 'any', src: ANY },
]

function normalizeLang(lang: string): string {
  const l = (lang || '').toLowerCase().trim()
  const map: Record<string, string> = {
    sh: 'bash', shell: 'bash', zsh: 'bash', console: 'bash', cmd: 'bash',
    yml: 'yaml', py: 'python', golang: 'go',
    javascript: 'js', typescript: 'js', ts: 'js', jsx: 'js', tsx: 'js',
    dockerfile: 'bash', docker: 'bash',
  }
  return map[l] || l
}

const cache = new Map<string, RegExp>()
function getRegex(key: string): RegExp {
  let re = cache.get(key)
  if (re) return re
  const defs = RULES[key] || DEFAULT
  const alt = defs.map(d => `(?<${d.name}>${d.src})`).join('|')
  // 'm' 让 prop 的 (?<=^|\s) 能识别行首; 'y' 保证每次从 pos 精确起匹配(单遍分词不重扫)
  re = new RegExp(alt, 'ym')
  cache.set(key, re)
  return re
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** 高亮单段代码: 返回 HTML 字符串(已转义)。raw 必须是未转义的原始代码。 */
export function highlightCode(raw: string, lang: string): string {
  if (!raw) return ''
  const key = normalizeLang(lang)
  const defs = RULES[key] || DEFAULT
  const re = getRegex(key)
  let out = ''
  let pos = 0
  while (pos < raw.length) {
    re.lastIndex = pos
    const m = re.exec(raw)
    if (!m || m.index !== pos || !m[0]) {
      // 兜底: 推进 1 字符(理论上 any 组必中, 这里仅防意外)
      out += esc(raw[pos])
      pos++
      continue
    }
    const text = m[0]
    let cls: TokenClass | undefined
    for (const d of defs) {
      if (d.cls !== undefined && m.groups?.[d.name] !== undefined) {
        cls = d.cls
        break
      }
    }
    out += cls ? `<span class="${cls}">${esc(text)}</span>` : esc(text)
    pos += text.length
  }
  return out
}

/**
 * 对容器内所有 <pre><code> 注入高亮。已处理(pre.dataset.hl)跳过, 避免流式重复高亮闪烁。
 * 语言取自 pre 上的 data-lang 属性或 code 的 language-xxx class。
 */
export function highlightAllCode(root: HTMLElement | null) {
  if (!root) return
  for (const pre of Array.from(root.querySelectorAll('pre'))) {
    if (pre.dataset.hl) continue
    const code = pre.querySelector('code')
    if (!code) continue
    const langAttr = pre.getAttribute('data-lang') || ''
    const langCls = code.className.match(/language-(\w+)/)?.[1] || ''
    const lang = (langAttr || langCls).toLowerCase()
    code.innerHTML = highlightCode(code.textContent || '', lang)
    pre.dataset.hl = '1'
  }
}
