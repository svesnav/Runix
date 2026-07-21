"use client";

import { Languages } from "lucide-react";
import { LOCALES, useLocale, type Locale } from "@/i18n";

export function LanguageSwitcher() {
  const { locale, setLocale } = useLocale();
  return (
    <label className="flex cursor-pointer items-center gap-2 rounded-md px-3 py-2 text-sm text-ink-dim hover:bg-card/60 hover:text-ink">
      <Languages size={15} />
      <select
        value={locale}
        onChange={(e) => setLocale(e.target.value as Locale)}
        className="w-full cursor-pointer bg-transparent text-sm outline-none"
        aria-label="Language"
      >
        {Object.entries(LOCALES).map(([code, { label }]) => (
          <option key={code} value={code} className="bg-panel text-ink">
            {label}
          </option>
        ))}
      </select>
    </label>
  );
}
