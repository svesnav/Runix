"use client";

import { clsx } from "clsx";

export function Tabs({
  items,
  value,
  onChange,
}: {
  items: { id: string; label: string }[];
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <div className="flex gap-1 border-b border-edge">
      {items.map((item) => (
        <button
          key={item.id}
          onClick={() => onChange(item.id)}
          className={clsx(
            "cursor-pointer border-b-2 px-3 py-2 text-sm font-medium transition-colors",
            value === item.id
              ? "border-brand text-ink"
              : "border-transparent text-ink-dim hover:text-ink",
          )}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
