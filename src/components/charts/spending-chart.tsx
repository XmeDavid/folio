"use client";

import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  Cell,
} from "recharts";
import { formatMoney } from "@/lib/utils";

const COLORS = [
  "#6366f1", "#8b5cf6", "#a78bfa", "#c4b5fd",
  "#818cf8", "#4f46e5", "#7c3aed", "#5b21b6",
  "#6d28d9", "#4338ca", "#3730a3", "#312e81",
  "#93c5fd", "#60a5fa", "#3b82f6", "#2563eb",
];

interface CategoryData {
  category: string;
  total: number;
  count: number;
}

export function SpendingChart({
  data,
  currency,
  height = 400,
}: {
  data: CategoryData[];
  currency: string;
  height?: number;
}) {
  if (!data || data.length === 0) {
    return (
      <div
        className="flex items-center justify-center text-text-tertiary text-sm font-mono"
        style={{ height }}
      >
        No spending data
      </div>
    );
  }

  // Show spending (negative values) as positive bars, sorted by magnitude
  const chartData = data
    .filter((d) => d.total < 0)
    .map((d) => ({
      category: d.category.includes(" // ") ? d.category.split(" // ")[1] : d.category,
      fullCategory: d.category,
      amount: Math.abs(d.total),
      count: d.count,
    }))
    .sort((a, b) => b.amount - a.amount)
    .slice(0, 15);

  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart
          data={chartData}
          layout="vertical"
          margin={{ top: 4, right: 20, left: 0, bottom: 4 }}
        >
          <CartesianGrid strokeDasharray="3 3" stroke="#1e2230" horizontal={false} />
          <XAxis
            type="number"
            tick={{ fontSize: 10, fill: "#5c6278", fontFamily: "var(--font-geist-mono)" }}
            stroke="#1e2230"
            tickFormatter={(v) => {
              if (v >= 1000) return `${(v / 1000).toFixed(0)}k`;
              return v.toFixed(0);
            }}
          />
          <YAxis
            type="category"
            dataKey="category"
            width={140}
            tick={{ fontSize: 10, fill: "#5c6278", fontFamily: "var(--font-geist-mono)" }}
            stroke="#1e2230"
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
            formatter={(value) => [formatMoney(Number(value), currency), "Spent"]}
            labelFormatter={(label) => String(label)}
          />
          <Bar dataKey="amount" radius={[0, 4, 4, 0]}>
            {chartData.map((_, i) => (
              <Cell key={i} fill={COLORS[i % COLORS.length]} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
