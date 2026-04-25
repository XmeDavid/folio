import * as React from "react";
import { cn } from "@/lib/utils";

export type DataTableColumn<T> = {
  header: React.ReactNode;
  cell: (row: T) => React.ReactNode;
  className?: string;
  headerClassName?: string;
  align?: "left" | "right";
};

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  rowClassName,
  isLoading,
  emptyMessage = "No results.",
  className,
}: {
  columns: DataTableColumn<T>[];
  rows: T[];
  rowKey: (row: T, index: number) => React.Key;
  rowClassName?: (row: T) => string | undefined;
  isLoading?: boolean;
  emptyMessage?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "overflow-hidden rounded-[12px] border border-border bg-surface",
        className
      )}
    >
      <table className="w-full text-left text-[14px]">
        <thead className="bg-surface-subtle text-[11px] font-medium tracking-[0.07em] text-fg-faint uppercase">
          <tr>
            {columns.map((col, i) => (
              <th
                key={i}
                className={cn(
                  "px-4 py-3 first:pl-5 last:pr-5",
                  col.align === "right" && "text-right",
                  col.headerClassName
                )}
              >
                {col.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && !isLoading ? (
            <tr>
              <td
                colSpan={columns.length}
                className="px-5 py-6 text-center text-[13px] text-fg-muted"
              >
                {emptyMessage}
              </td>
            </tr>
          ) : null}
          {rows.map((row, i) => (
            <tr
              key={rowKey(row, i)}
              className={cn(
                "border-t border-border transition-colors hover:bg-surface-subtle",
                rowClassName?.(row)
              )}
            >
              {columns.map((col, ci) => (
                <td
                  key={ci}
                  className={cn(
                    "px-4 py-3 first:pl-5 last:pr-5",
                    col.align === "right" && "text-right",
                    col.className
                  )}
                >
                  {col.cell(row)}
                </td>
              ))}
            </tr>
          ))}
          {isLoading ? (
            <tr>
              <td
                colSpan={columns.length}
                className="px-5 py-6 text-center text-[13px] text-fg-muted"
              >
                Loading...
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}
