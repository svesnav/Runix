"use client";

import * as echarts from "echarts";
import { useEffect, useRef } from "react";

// Minimal ECharts React binding; avoids a wrapper dependency.
export function EChart({ option, className }: { option: echarts.EChartsOption; className?: string }) {
  const holder = useRef<HTMLDivElement>(null);
  const chart = useRef<echarts.ECharts | null>(null);

  useEffect(() => {
    if (!holder.current) return;
    chart.current = echarts.init(holder.current);
    const onResize = () => chart.current?.resize();
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      chart.current?.dispose();
      chart.current = null;
    };
  }, []);

  useEffect(() => {
    chart.current?.setOption(option, { notMerge: true });
  }, [option]);

  return <div ref={holder} className={className} />;
}
