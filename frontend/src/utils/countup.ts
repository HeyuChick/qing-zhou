import { ref, watch, onScopeDispose, type Ref } from 'vue'

function prefersReducedMotion(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
}

/**
 * 数字滚动动画：源值变化时用 ease-out-cubic 从当前显示值滚到新值。
 *
 * 数据是异步到达的，`immediate: true` 让第一帧就从 0 起跳，正好和卡片的入场
 * 动画错开——直接把终值拍上去会让整块 KPI 看起来是「闪」出来的。
 *
 * 从当前显示值（而不是从 0）重新起跳，所以切换筛选条件时是接着滚，不会回零。
 *
 * `round: false` 保留小数，给 CPU/内存百分比这类本身就带小数的指标用。
 */
export function useCountUp(src: () => number, opts: { duration?: number; round?: boolean } = {}): Ref<number> {
  const duration = opts.duration ?? 650
  const round = opts.round !== false
  const disp = ref(0)
  let raf = 0
  let guard = 0

  const stop = () => { cancelAnimationFrame(raf); clearTimeout(guard) }
  const snap = (v: number) => { disp.value = round ? Math.round(v) : v }

  watch(src, (to) => {
    stop()
    // NaN/Infinity 会把 disp 永久污染成 NaN（后续每一帧都从 NaN 起算），归零挡掉。
    const target = Number.isFinite(to) ? to : 0
    if (prefersReducedMotion()) { snap(target); return }

    const from = Number.isFinite(disp.value) ? disp.value : 0
    const start = performance.now()
    const tick = (now: number) => {
      const p = Math.min((now - start) / duration, 1)
      const e = 1 - Math.pow(1 - p, 3)
      snap(from + (target - from) * e)
      if (p < 1) raf = requestAnimationFrame(tick)
      else clearTimeout(guard)
    }
    raf = requestAnimationFrame(tick)

    // rAF 在后台标签页 / 未合成的窗口里根本不会被调用，动画就永远停在起点——
    // 页面上留下的是一个「0 积分」，而不是真实余额。兜底定时器保证不管动画有没有
    // 跑起来，到点都会落到真值上。setTimeout 在后台会被降频但不会被冻结。
    guard = window.setTimeout(() => { cancelAnimationFrame(raf); snap(target) }, duration + 120)
  }, { immediate: true })

  // 组件卸载时停掉动画：否则在滚动中途切走，rAF 还会一路跑到 650ms 结束，
  // 每帧都往一个再也不会被渲染的 ref 上写值。
  onScopeDispose(stop)

  return disp
}
