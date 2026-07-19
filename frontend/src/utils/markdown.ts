/**
 * 简易 Markdown → HTML 转换器 (XSS 安全，支持 GFM 表格)
 * 移植自原 web/dist/app.js 中的 mdToHtml
 */

// esc MUST escape quotes too: URLs from [text](url) / ![alt](img) are
// interpolated into href="…" / src="…" attributes, so an unescaped double quote
// would break out of the attribute and inject an event handler (e.g.
// [x](http://a"onmouseover="alert(1)) → stored XSS). Escaping " and ' closes it.
const esc = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;')

const safeUrl = (u: string) => /^(https?:\/\/|mailto:|\/)/i.test(u.replace(/&amp;/g, '&'))

export function mdToHtml(src: string): string {
  if (!src) return ''
  src = String(src).replace(/\r\n?/g, '\n')

  const holds: string[] = []
  const blockIdx = new Set<number>()
  const hold = (html: string, block?: boolean) => {
    holds.push(html)
    if (block) blockIdx.add(holds.length - 1)
    return '\x00' + (holds.length - 1) + '\x00'
  }

  // 1) fenced code blocks
  src = src.replace(/```[ \t]*[\w-]*\n?([\s\S]*?)```/g, (_, code) =>
    hold('<pre class="md-pre"><code>' + esc(code.replace(/\n$/, '')) + '</code></pre>', true)
  )
  src = esc(src)

  // 2) inline code
  src = src.replace(/`([^`\n]+)`/g, (_, c) => hold('<code class="md-code">' + c + '</code>'))

  // 3) images then links
  src = src.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (_, alt, u) =>
    safeUrl(u) ? hold('<img src="' + u + '" alt="' + alt + '">') : _
  )
  src = src.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, t, u) =>
    safeUrl(u) ? hold('<a href="' + u + '" target="_blank" rel="noopener noreferrer">' + t + '</a>') : t
  )

  // 4) bare-URL autolink
  src = src.replace(/(^|[\s(])(https?:\/\/[^\s<)]+)/g, (_, pre, u) => {
    const trail = (u.match(/[.,;:!?)]+$/) || [''])[0]
    const link = u.slice(0, u.length - trail.length)
    return pre + hold('<a href="' + link + '" target="_blank" rel="noopener noreferrer">' + link + '</a>') + trail
  })

  // 5) headings
  src = src.replace(/^(#{1,6})[ \t]+(.*)$/gm, (_, h, t) =>
    '<h' + h.length + ' class="md-h">' + t + '</h' + h.length + '>'
  )

  // horizontal rule
  src = src.replace(/^[ \t]*([-*_])(?:[ \t]*\1){2,}[ \t]*$/gm, '<hr>')

  // blockquote
  src = src.replace(/(?:^&gt;[ \t]?.*(?:\n|$))+/gm, m =>
    '<blockquote>' + m.replace(/^&gt;[ \t]?/gm, '').trim().replace(/\n/g, '<br>') + '</blockquote>\n'
  )

  // bold
  src = src.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>')
  // italic
  src = src.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>')

  // 6) GFM tables
  src = mdTables(src, hold)

  // 7) unordered lists
  src = src.replace(/(?:^[ \t]*[-*+][ \t]+.*(?:\n|$))+/gm, m =>
    '<ul class="md-ul">' + m.trimEnd().split('\n').map(l => {
      const item = l.replace(/^[ \t]*[-*+][ \t]+/, '')
      const t = item.match(/^\[([ xX])\][ \t]+(.*)$/)
      if (t) return '<li class="md-task"><input type="checkbox" disabled' + (t[1] === ' ' ? '' : ' checked') + '><span>' + t[2] + '</span></li>'
      return '<li>' + item + '</li>'
    }).join('') + '</ul>\n'
  )

  // 8) ordered lists
  src = src.replace(/(?:^[ \t]*\d+\.[ \t]+.*(?:\n|$))+/gm, m =>
    '<ol class="md-ol">' + m.trimEnd().split('\n').map(l =>
      '<li>' + l.replace(/^[ \t]*\d+\.[ \t]+/, '') + '</li>'
    ).join('') + '</ol>\n'
  )

  // 9) paragraphs
  let html = src.split(/\n{2,}/).map(p => {
    p = p.trim()
    if (!p) return ''
    if (/^<[a-z]/.test(p)) return p
    return '<p>' + p.replace(/\n/g, '<br>') + '</p>'
  }).join('\n')

  // Restore placeholders repeatedly: a held chunk can itself contain a
  // placeholder — inline code or a link inside a table cell is held first, then
  // the whole table is held around it — so a single pass leaves the inner marker
  // visible as a stray digit. Bounded so a self-referencing hold can't spin.
  for (let pass = 0; pass < 5 && /\x00\d+\x00/.test(html); pass++) {
    html = html.replace(/\x00(\d+)\x00/g, (_, i) => holds[parseInt(i)] || '')
  }
  return html
}

function mdTables(src: string, hold: (h: string, b?: boolean) => string): string {
  // The last row needs no trailing newline: a table at the very end of a
  // document otherwise lost its final row, which leaked out as literal "| a | b |".
  return src.replace(/(?:^[ \t]*\|.+\|[ \t]*(?:\n|$))+/gm, block => {
    const rows = block.trimEnd().split('\n')
    if (rows.length < 2) return block

    const parseRow = (row: string) =>
      row.replace(/^\|[ \t]*/, '').replace(/[ \t]*\|$/, '').split(/\s*\|\s*/)

    const headers = parseRow(rows[0])
    // check separator row
    if (!/^\|?[\s\-:|]+\|?$/.test(rows[1])) return block

    let html = '<div class="md-table-wrap"><table class="md-table"><thead><tr>'
    headers.forEach(h => html += '<th>' + h + '</th>')
    html += '</tr></thead><tbody>'
    for (let i = 2; i < rows.length; i++) {
      const cells = parseRow(rows[i])
      html += '<tr>'
      cells.forEach(c => html += '<td>' + c + '</td>')
      html += '</tr>'
    }
    html += '</tbody></table></div>'
    return hold(html, true)
  })
}
