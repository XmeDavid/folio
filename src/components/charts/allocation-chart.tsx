"use client";

import {
  PieChart,
  Pie,
  Cell,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { formatMoney } from "@/lib/utils";

interface HoldingSlice {
  ticker: string;
  currentValue: number;
}

const COLORS = [
  "#6c9cff", "#3dd68c", "#ffc145", "#ff5c6c", "#a78bfa",
  "#f472b6", "#34d399", "#60a5fa", "#facc15", "#f87171",
  "#818cf8", "#fb923c", "#38bdf8", "#4ade80", "#c084fc",
];

export function AllocationChart({
  holdings,
  currency,
}: {
  holdings: HoldingSlice[];
  currency: string;
}) {
  const sorted = [...holdings]
    .filter((h) => h.currentValue > 0)
    .sort((a, b) => b.currentValue - a.currentValue);

  const top10 = sorted.slice(0, 10);
  const rest = sorted.slice(10);
  const data = rest.length > 0
    ? [...top10, { ticker: "Others", currentValue: rest.reduce((s, h) => s + h.currentValue, 0) }]
    : top10;

  return (
    <div className="h-[280px]">
      <ResponsiveContainer width="100%" height="100%">
        <PieChart>
          <Pie
            data={data}
            dataKey="currentValue"
            nameKey="ticker"
            cx="50%"
            cy="50%"
            innerRadius={60}
            outerRadius={100}
            paddingAngle={2}
            stroke="none"
          >
            {data.map((_, i) => (
              <Cell key={i} fill={COLORS[i % COLORS.length]} />
            ))}
          </Pie>
          <Tooltip
            contentStyle={{
              background: "#1a1e2a",
              border: "1px solid #2a2f3e",
              borderRadius: "8px",
              fontSize: "12px",
              color: "#e8eaf0",
            }}
            itemStyle={{ color: "#e8eaf0" }}
            labelStyle={{ color: "#8b91a3" }}
            formatter={(value) => formatMoney(Number(value), currency)}
          />
        </PieChart>
      </ResponsiveContainer>
      <div className="flex flex-wrap gap-x-4 gap-y-1.5 mt-2 px-2">
        {data.map((d, i) => (
          <div key={d.ticker} className="flex items-center gap-1.5 text-xs text-text-secondary">
            <div
              className="w-2.5 h-2.5 rounded-sm shrink-0"
              style={{ background: COLORS[i % COLORS.length] }}
            />
            <span className="font-mono">{d.ticker}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
