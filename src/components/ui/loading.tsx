export function LoadingSpinner({ className }: { className?: string }) {
  return (
    <div className={`flex items-center justify-center ${className || ""}`}>
      <div className="flex gap-1.5">
        <div className="w-2 h-2 rounded-full bg-accent animate-pulse-soft" />
        <div className="w-2 h-2 rounded-full bg-accent animate-pulse-soft stagger-1" />
        <div className="w-2 h-2 rounded-full bg-accent animate-pulse-soft stagger-2" />
      </div>
    </div>
  );
}

export function LoadingCard() {
  return (
    <div className="rounded-xl border border-border-subtle bg-bg-secondary p-5 animate-pulse-soft">
      <div className="h-3 bg-bg-tertiary rounded w-24 mb-3" />
      <div className="h-8 bg-bg-tertiary rounded w-40 mb-2" />
      <div className="h-3 bg-bg-tertiary rounded w-20" />
    </div>
  );
}
