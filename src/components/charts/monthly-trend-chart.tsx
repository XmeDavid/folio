"use client";

import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  ReferenceLine,
  Legend,
} from "recharts";
import { formatMoney } from "@/lib/utils";

interface MonthlyData {
  month: string;
  spending: number;
  income: number;
  net: number;
}

export function MonthlyTrendChart({
  data,
  currency,
  height = 320,
  onBarClick,
}: {
  data: MonthlyData[];
  currency: string;
  height?: number;
  onBarClick?: (month: string) => void;
}) {
  if (!data || data.length === 0) {
    return (
      <div
        className="flex items-center justify-center text-text-tertiary text-sm font-mono"
        style={{ height }}
      >
        No monthly data
      </div>
    );
  }

  const chartData = data.map((d) => ({
    month: d.month,
    Income: d.income,
    Spending: Math.abs(d.spending),
    Net: d.net,
  }));

  const formatMonth = (val: string) => {
    const [year, month] = val.split("-");
    const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    return `${months[parseInt(month) - 1]} '${year.slice(2)}`;
  };

  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={chartData} margin={{ top: 4, right: 4, left: 0, bottom: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#1e2230" vertical={false} />
          <XAxis
            dataKey="month"
            tickFormatter={formatMonth}
            tick={{ fontSize: 10, fill: "#5c6278", fontFamily: "var(--font-geist-mono)" }}
            stroke="#1e2230"
            interval="preserveStartEnd"
            minTickGap={40}
          />
          <YAxis
            tick={{ fontSize: 10, fill: "#5c6278", fontFamily: "var(--font-geist-mono)" }}
            stroke="#1e2230"
            width={50}
            tickFormatter={(v) => {
              if (Math.abs(v) >= 1000) return `${(v / 1000).toFixed(0)}k`;
              return v.toFixed(0);
            }}
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
            labelFormatter={(label) => formatMonth(String(label))}
            formatter={(value, name) => [
              formatMoney(Number(value), currency),
              String(name),
            ]}
          />
          <Legend
            wrapperStyle={{ fontSize: "11px", fontFamily: "var(--font-geist-mono)" }}
          />
          <ReferenceLine y={0} stroke="#2a2f3e" strokeDasharray="3 3" />
          <Bar
            dataKey="Income"
            fill="#22c55e"
            radius={[2, 2, 0, 0]}
            cursor={onBarClick ? "pointer" : undefined}
            onClick={(_data: any, _idx: number, e: any) => onBarClick?.(e?.month ?? _data?.month ?? _data?.payload?.month)}
          />
          <Bar
            dataKey="Spending"
            fill="#ef4444"
            radius={[2, 2, 0, 0]}
            cursor={onBarClick ? "pointer" : undefined}
            onClick={(_data: any, _idx: number, e: any) => onBarClick?.(e?.month ?? _data?.month ?? _data?.payload?.month)}
          />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
