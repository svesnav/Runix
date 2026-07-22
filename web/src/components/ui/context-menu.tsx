"use client";

import { clsx } from "clsx";
import { useEffect, useLayoutEffect, useRef, useState } from "react";

export interface MenuItem {
  label: string;
  icon?: React.ReactNode;
  onSelect: () => void;
  disabled?: boolean;
  danger?: boolean;
  separatorBefore?: boolean;
}

// ContextMenu renders a floating menu at viewport coordinates, flipping when
// it would overflow. Closes on outside click, Escape, scroll or resize.
export function ContextMenu({
  x,
  y,
  items,
  onClose,
}: {
  x: number;
  y: number;
  items: MenuItem[];
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x, y });

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    setPos({
      x: x + rect.width > window.innerWidth ? Math.max(4, window.innerWidth - rect.width - 4) : x,
      y: y + rect.height > window.innerHeight ? Math.max(4, window.innerHeight - rect.height - 4) : y,
    });
  }, [x, y]);

  useEffect(() => {
    const close = () => onClose();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("click", close);
    window.addEventListener("contextmenu", close);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("contextmenu", close);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  return (
    <div
      ref={ref}
      style={{ left: pos.x, top: pos.y }}
      onClick={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.preventDefault()}
      className="fixed z-50 min-w-48 rounded-md border border-edge bg-panel py-1 shadow-2xl"
    >
      {items.map((item, i) => (
        <div key={i}>
          {item.separatorBefore && <div className="my-1 h-px bg-edge" />}
          <button
            disabled={item.disabled}
            onClick={() => {
              if (item.disabled) return;
              onClose();
              item.onSelect();
            }}
            className={clsx(
              "flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
              item.disabled
                ? "cursor-not-allowed text-ink-dim/40"
                : item.danger
                  ? "cursor-pointer text-err hover:bg-err/10"
                  : "cursor-pointer text-ink hover:bg-card",
            )}
          >
            <span className="flex w-4 justify-center text-ink-dim">
            </span>
            {item.label}
          </button>
        </div>
      ))}
    </div>
  );
}
