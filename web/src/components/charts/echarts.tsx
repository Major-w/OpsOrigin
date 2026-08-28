
import React, { useRef, useEffect, useCallback } from 'react';
import ReactEChartsCore from 'echarts-for-react/lib/core';
import * as echarts from 'echarts/core';
import { CanvasRenderer } from 'echarts/renderers';
import {
  LineChart,
  BarChart,
  PieChart,
  ScatterChart,
  GaugeChart,
  HeatmapChart,
  TreemapChart,
  SankeyChart,
  GraphChart,
  CandlestickChart,
} from 'echarts/charts';
import {
  TitleComponent,
  TooltipComponent,
  LegendComponent,
  GridComponent,
  DataZoomComponent,
  VisualMapComponent,
  ToolboxComponent,
  MarkLineComponent,
  MarkPointComponent,
  MarkAreaComponent,
  GraphicComponent,
  DatasetComponent,
  TransformComponent,
} from 'echarts/components';
import type { EChartsOption } from 'echarts';

// Register all needed components once
echarts.use([
  CanvasRenderer,
  LineChart,
  BarChart,
  PieChart,
  ScatterChart,
  GaugeChart,
  HeatmapChart,
  TreemapChart,
  SankeyChart,
  GraphChart,
  CandlestickChart,
  TitleComponent,
  TooltipComponent,
  LegendComponent,
  GridComponent,
  DataZoomComponent,
  VisualMapComponent,
  ToolboxComponent,
  MarkLineComponent,
  MarkPointComponent,
  MarkAreaComponent,
  GraphicComponent,
  DatasetComponent,
  TransformComponent,
]);

// ---- light / dark theme presets ----
const lightTheme: Record<string, unknown> = {
  color: ['#3B82F6', '#60A5FA', '#2563EB', '#f59e0b', '#ef4444', '#8b5cf6', '#06b6d4'],
  backgroundColor: 'transparent',
  textStyle: { color: '#64748b' },
  title: { textStyle: { color: '#1e293b' } },
  legend: { textStyle: { color: '#64748b' } },
  tooltip: {
    backgroundColor: 'rgba(255,255,255,0.95)',
    borderColor: '#e2e8f0',
    textStyle: { color: '#1e293b' },
  },
  grid: { borderColor: '#e2e8f0' },
};

const darkTheme: Record<string, unknown> = {
  color: ['#60A5FA', '#3B82F6', '#93C5FD', '#fbbf24', '#f87171', '#a78bfa', '#22d3ee'],
  backgroundColor: 'transparent',
  textStyle: { color: '#94a3b8' },
  title: { textStyle: { color: '#e2e8f0' } },
  legend: { textStyle: { color: '#94a3b8' } },
  tooltip: {
    backgroundColor: 'rgba(15,23,42,0.95)',
    borderColor: 'rgba(255,255,255,0.08)',
    textStyle: { color: '#e2e8f0' },
  },
  grid: { borderColor: 'rgba(255,255,255,0.06)' },
};

// Register custom themes
echarts.registerTheme('ops-light', lightTheme);
echarts.registerTheme('ops-dark', darkTheme);

// ---- responsive chart type helpers ----
export interface ChartMetric {
  name: string;
  data: number[];
  color?: string;
  areaStyle?: boolean;
}

export interface ChartTimeSeriesProps {
  xAxisData: string[];
  metrics: ChartMetric[];
  title?: string;
  height?: number | string;
  smooth?: boolean;
  showDataZoom?: boolean;
  yAxisLabel?: string;
}

export interface ChartGaugeProps {
  value: number;
  max?: number;
  title?: string;
  height?: number | string;
  colorStops?: [number, string][];
}

export interface ChartPieProps {
  data: { name: string; value: number }[];
  title?: string;
  height?: number | string;
  roseType?: boolean;
}

// ---- hook: detect dark mode ----
function useIsDark(): boolean {
  const [dark, setDark] = React.useState(false);

  useEffect(() => {
    const check = () => setDark(document.documentElement.classList.contains('dark'));
    check();
    const obs = new MutationObserver(check);
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => obs.disconnect();
  }, []);

  return dark;
}

// ---- base ECharts wrapper ----
interface EChartsWrapperProps {
  option: EChartsOption;
  height?: number | string;
  style?: React.CSSProperties;
  className?: string;
  notMerge?: boolean;
  lazyUpdate?: boolean;
}

export function EChartsWrapper({
  option,
  height = 320,
  style,
  className,
  notMerge = true,
  lazyUpdate = false,
}: EChartsWrapperProps) {
  const isDark = useIsDark();
  const ref = useRef<ReactEChartsCore>(null);

  useEffect(() => {
    ref.current?.getEchartsInstance()?.resize();
  }, []);

  const mergedOption: EChartsOption = {
    ...option,
    // tooltip default if not provided
    tooltip: option.tooltip ?? { trigger: 'axis' },
  };

  return (
    <ReactEChartsCore
      ref={ref}
      echarts={echarts}
      option={mergedOption}
      theme={isDark ? 'ops-dark' : 'ops-light'}
      style={{ width: '100%', height, ...style }}
      className={className}
      notMerge={notMerge}
      lazyUpdate={lazyUpdate}
      opts={{ renderer: 'canvas' }}
    />
  );
}

// ---- preset charts ----

/** Time-series line/area chart — ideal for CPU / memory / network trends */
export function TimeSeriesChart({
  xAxisData,
  metrics,
  title,
  height = 320,
  smooth = true,
  showDataZoom = true,
  yAxisLabel,
}: ChartTimeSeriesProps) {
  const option: EChartsOption = {
    title: title
      ? { text: title, left: 'center', textStyle: { fontSize: 14, fontWeight: 600 } }
      : undefined,
    tooltip: { trigger: 'axis' },
    legend: metrics.length > 1 ? { bottom: 0, data: metrics.map((m) => m.name) } : undefined,
    grid: { top: title ? 48 : 16, left: 48, right: 16, bottom: showDataZoom ? 60 : 40 },
    xAxis: {
      type: 'category',
      data: xAxisData,
      boundaryGap: false,
      axisLabel: { fontSize: 11 },
    },
    yAxis: {
      type: 'value',
      name: yAxisLabel,
      axisLabel: { fontSize: 11 },
    },
    dataZoom: showDataZoom
      ? [{ type: 'inside' }, { type: 'slider', bottom: 28, height: 16 }]
      : undefined,
    series: metrics.map((m) => ({
      name: m.name,
      type: 'line',
      data: m.data,
      smooth,
      showSymbol: false,
      areaStyle: m.areaStyle ? {} : undefined,
      lineStyle: m.color ? { color: m.color } : undefined,
      itemStyle: m.color ? { color: m.color } : undefined,
    })),
  };

  return <EChartsWrapper option={option} height={height} />;
}

/** Gauge — for single-metric dashboards (disk usage, SLA, health score) */
export function GaugeChartView({ value, max = 100, title, height = 280 }: ChartGaugeProps) {
  const option: EChartsOption = {
    title: title
      ? { text: title, left: 'center', bottom: 0, textStyle: { fontSize: 13, fontWeight: 500 } }
      : undefined,
    series: [
      {
        type: 'gauge',
        startAngle: 210,
        endAngle: -30,
        center: ['50%', '55%'],
        radius: '85%',
        min: 0,
        max,
        axisLine: {
          lineStyle: {
            width: 20,
            color: [
              [0.3, '#3B82F6'],
              [0.7, '#f59e0b'],
              [1, '#ef4444'],
            ],
          },
        },
        pointer: { length: '60%', width: 6 },
        detail: {
          fontSize: 28,
          fontWeight: 700,
          formatter: '{value}%',
          offsetCenter: [0, '60%'],
        },
        data: [{ value }],
      },
    ],
  };

  return <EChartsWrapper option={option} height={height} />;
}

/** Pie / rose chart — for distribution (SLA, resource allocation, error types) */
export function PieChartView({ data, title, height = 320, roseType = false }: ChartPieProps) {
  const option: EChartsOption = {
    title: title
      ? { text: title, left: 'center', textStyle: { fontSize: 14, fontWeight: 600 } }
      : undefined,
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, type: 'scroll' },
    series: [
      {
        type: 'pie',
        radius: roseType ? ['20%', '70%'] : '65%',
        center: ['50%', '50%'],
        roseType: roseType ? 'radius' : undefined,
        itemStyle: { borderRadius: 4, borderColor: 'transparent', borderWidth: 2 },
        label: { show: false },
        emphasis: {
          label: { show: true, fontSize: 14, fontWeight: 'bold' },
        },
        data,
      },
    ],
  };

  return <EChartsWrapper option={option} height={height} />;
}

/** Bar chart — for comparisons (agent load, task throughput, top-N) */
export function BarChartView({
  xAxisData,
  metrics,
  title,
  height = 320,
  horizontal = false,
}: ChartTimeSeriesProps & { horizontal?: boolean }) {
  const xKey = horizontal ? 'yAxis' : 'xAxis';
  const yKey = horizontal ? 'xAxis' : 'yAxis';

  const option: EChartsOption = {
    title: title
      ? { text: title, left: 'center', textStyle: { fontSize: 14, fontWeight: 600 } }
      : undefined,
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    legend: metrics.length > 1 ? { bottom: 0 } : undefined,
    grid: { top: title ? 48 : 16, left: 48, right: 16, bottom: 40 },
    [xKey]: { type: 'category', data: xAxisData, axisLabel: { fontSize: 11 } },
    [yKey]: { type: 'value', axisLabel: { fontSize: 11 } },
    series: metrics.map((m) => ({
      name: m.name,
      type: 'bar',
      data: m.data,
      barMaxWidth: 40,
      itemStyle: { borderRadius: [4, 4, 0, 0] },
    })),
  };

  return <EChartsWrapper option={option} height={height} />;
}

export { echarts };
export type { EChartsOption };
