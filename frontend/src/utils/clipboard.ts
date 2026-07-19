// copyText copies text to the clipboard and reports whether it actually
// succeeded. navigator.clipboard exists only on secure (HTTPS / localhost)
// origins — self-hosted panels are frequently plain HTTP, where it is undefined —
// so this falls back to a legacy execCommand copy and returns false on failure,
// letting callers show an honest error instead of a fake "已复制".
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch { /* secure-context copy blocked — fall through to legacy path */ }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}
