<template>
  <div ref="el" class="trend-chart" />
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted } from 'vue'
import * as echarts from 'echarts'

const props = defineProps<{
  data: { date?: string; up?: number; down?: number }[]
}>()

const el = ref<HTMLElement | null>(null)
let chart: echarts.ECharts | null = null

function draw() {
  if (!el.value || !el.value.clientWidth) return
  if (!chart) chart = echarts.init(el.value)
  const x = props.data.map((d: any) => (d.date || '').slice(5))
  const mk = (name: string, key: 'up' | 'down', color: string) => ({
    name, type: 'line', smooth: 0.35, stack: 'total', showSymbol: false,
    lineStyle: { width: 1.6, color },
    areaStyle: {
      color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
        { offset: 0, color: color + 'cc' }, { offset: 1, color: color + '08' },
      ]),
    },
    data: props.data.map((d: any) => d[key] || 0),
  })
  chart.setOption({
    grid: { left: 8, right: 12, top: 26, bottom: 4, containLabel: true },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v: number) => fmtBytes(v),
      axisPointer: { type: 'line', lineStyle: { color: '#c9c9c9' } },
    },
    legend: { data: ['上行', '下行'], right: 0, top: 0, icon: 'roundRect', itemWidth: 9, itemHeight: 9,
      textStyle: { color: '#595959', fontSize: 11 } },
    xAxis: { type: 'category', boundaryGap: false, data: x,
      axisLine: { lineStyle: { color: '#e5e5e5' } }, axisTick: { show: false },
      axisLabel: { color: '#767676', fontSize: 11 } },
    yAxis: { type: 'value', splitLine: { lineStyle: { color: '#f1f1f1' } },
      axisLine: { show: false }, axisTick: { show: false },
      axisLabel: { color: '#767676', fontSize: 11, formatter: (v: number) => fmtBytes(v) } },
    series: [mk('上行', 'up', '#6f8f76'), mk('下行', 'down', '#5e7a99')],
  }, true)
}

watch(() => props.data, draw, { deep: true })
function onResize() { chart?.resize() }
onMounted(() => {
  draw()
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  chart?.dispose()
  chart = null
})

function fmtBytes(n: number | null | undefined): string {
  if (n == null || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  n = Math.abs(n)
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
  return (n < 10 && i > 0 ? n.toFixed(2) : n < 100 && i > 0 ? n.toFixed(1) : Math.round(n).toString()) + ' ' + units[i]
}
</script>

<style scoped>
.trend-chart { width: 100%; height: 260px; }
</style>
