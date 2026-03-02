"use client";

import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  ReferenceLine,
} from "recharts";
import { formatMoney } from "@/lib/utils";

interface SeriesConfig {
  key: string;
  color: string;
  name: string;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function TimeSeriesChart({
  data,
  series,
  currency,
  height = 320,
}: {
  data: any[];
  series: SeriesConfig[];
  currency: string;
  height?: number;
}) {
  if (!data || data.length === 0) {
    return (
      <div
        className="flex items-center justify-center text-text-tertiary text-sm font-mono"
        style={{ height }}
      >
        No data available
      </div>
    );
  }

  const formatDate = (val: string) => {
    const d = new Date(val);
    return d.toLocaleDateString("en-GB", { month: "short", year: "2-digit" });
  };

  const formatTick = (val: number) => {
    if (Math.abs(val) >= 1000) return `${(val / 1000).toFixed(1)}k`;
    return val.toFixed(0);
  };

  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`grad-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.25} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="#1e2230" vertical={false} />
          <XAxis
            dataKey="date"
            tickFormatter={formatDate}
            tick={{ fontSize: 10, fill: "#5c6278", fontFamily: "var(--font-geist-mono)" }}
            stroke="#1e2230"
            interval="preserveStartEnd"
            minTickGap={60}
          />
          <YAxis
            tickFormatter={formatTick}
            tick={{ fontSize: 10, fill: "#5c6278", fontFamily: "var(--font-geist-mono)" }}
            stroke="#1e2230"
            width={50}
          />
          <Tooltip
            contentStyle={{
              background: "#1a1e2a",
              border: "1px solid #2a2f3e",
              borderRadius: "8px",
              fontSize: "12px",
              color: "#e8eaf0",
              fontFamily: "var(--font-geist-mono)",
            }}
            labelFormatter={(label) => {
              const d = new Date(String(label));
              return d.toLocaleDateString("en-GB", {
                day: "2-digit",
                month: "short",
                year: "numeric",
              });
            }}
            formatter={(value, name) => [
              formatMoney(Number(value), currency),
              String(name),
            ]}
          />
          <ReferenceLine y={0} stroke="#2a2f3e" strokeDasharray="3 3" />
          {series.map((s) => (
            <Area
              key={s.key}
              type="monotone"
              dataKey={s.key}
              name={s.name}
              stroke={s.color}
              fill={`url(#grad-${s.key})`}
              strokeWidth={1.5}
              dot={false}
              activeDot={{ r: 3, fill: s.color, stroke: "#0c0e14", strokeWidth: 2 }}
            />
          ))}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
