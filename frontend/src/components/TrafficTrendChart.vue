<template>
  <div ref="el" class="trend-chart" />
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted } from 'vue'
import * as echarts from 'echarts'
import { fmtBytes } from '@/utils/format'

const props = defineProps<{
  data: { date?: string; up?: number; down?: number }[]
}>()

const el = ref<HTMLElement | null>(null)
let chart: echarts.ECharts | null = null
let ro: ResizeObserver | null = null

const UP = '#6f8f76'
const DOWN = '#5e7a99'

function draw() {
  if (!el.value || !el.value.clientWidth) return
  if (!chart) chart = echarts.init(el.value)
  const x = props.data.map((d: any) => (d.date || '').slice(5))
  const mk = (name: string, key: 'up' | 'down', color: string) => ({
    name, type: 'line', smooth: 0.35, stack: 'total', showSymbol: false,
    symbolSize: 6,
    lineStyle: { width: 1.6, color },
    itemStyle: { color },
    // 悬停时把另一条线淡出，堆叠面积图里两条挤在一起时才分得清看的是哪条
    emphasis: { focus: 'series' },
    areaStyle: {
      color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
        { offset: 0, color: color + 'cc' }, { offset: 1, color: color + '08' },
      ]),
    },
    data: props.data.map((d: any) => d[key] || 0),
  })
  chart.setOption({
    animationDuration: 700,
    animationEasing: 'cubicOut',
    // 数据换挡（7天↔30天）时按点位依次更新，整条线是「长」出来的而不是跳变
    animationDurationUpdate: 450,
    animationEasingUpdate: 'cubicOut',
    grid: { left: 8, right: 12, top: 26, bottom: 4, containLabel: true },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'line', lineStyle: { color: '#c9c9c9' } },
      borderWidth: 0,
      textStyle: { fontSize: 12 },
      // 堆叠图的重点是当天总量，但 ECharts 只会逐条列出分量——手写一行合计。
      formatter: (ps: any[]) => {
        if (!ps || !ps.length) return ''
        const rows = ps.map(p => `${p.marker}${p.seriesName} <b>${fmtBytes(p.value)}</b>`).join('<br/>')
        const total = ps.reduce((s, p) => s + (p.value || 0), 0)
        return `${ps[0].axisValue}<br/>${rows}<br/>合计 <b>${fmtBytes(total)}</b>`
      },
    },
    legend: {
      data: ['上行', '下行'], right: 0, top: 0, icon: 'roundRect', itemWidth: 9, itemHeight: 9,
      textStyle: { color: '#595959', fontSize: 11 },
    },
    xAxis: {
      type: 'category', boundaryGap: false, data: x,
      axisLine: { lineStyle: { color: '#e5e5e5' } }, axisTick: { show: false },
      // 30 天档位标签会挤成一团，交给 ECharts 按可用宽度自己抽稀
      axisLabel: { color: '#767676', fontSize: 11, hideOverlap: true },
    },
    yAxis: {
      type: 'value', splitLine: { lineStyle: { color: '#f1f1f1' } },
      axisLine: { show: false }, axisTick: { show: false },
      axisLabel: { color: '#767676', fontSize: 11, formatter: (v: number) => fmtBytes(v) },
    },
    series: [mk('上行', 'up', UP), mk('下行', 'down', DOWN)],
  }, true)
}

watch(() => props.data, draw, { deep: true })

onMounted(() => {
  draw()
  // ResizeObserver 而不是 window resize：侧边栏折叠、卡片布局重排这些不会触发
  // window resize，但图表容器确实变宽了。它同时兜住了「挂载时容器宽度还是 0，
  // draw() 直接 return」的情况——拿到宽度的那一刻会再画一次。
  if (el.value && typeof ResizeObserver !== 'undefined') {
    ro = new ResizeObserver(() => { chart ? chart.resize() : draw() })
    ro.observe(el.value)
  }
})

onUnmounted(() => {
  ro?.disconnect()
  ro = null
  chart?.dispose()
  chart = null
})
</script>

<style scoped>
.trend-chart { width: 100%; height: 260px; }
</style>
