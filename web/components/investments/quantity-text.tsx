import { formatQuantity } from "@/lib/format";

export function QuantityText({
  value,
  className,
}: {
  value: string | number | null | undefined;
  className?: string;
}) {
  return (
    <span className={className}>
      <span className="xl:hidden">
        {formatQuantity(value, { maxFractionDigits: 2 })}
      </span>
      <span className="hidden xl:inline">
        {formatQuantity(value, { maxFractionDigits: 4 })}
      </span>
    </span>
  );
}
