"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { en, type Dictionary } from "./en";
import { ru } from "./ru";

export const LOCALES = {
  en: { label: "English", dict: en },
  ru: { label: "Русский", dict: ru },
} as const;

export type Locale = keyof typeof LOCALES;

const STORAGE_KEY = "runix-locale";

interface I18nValue {
  locale: Locale;
  setLocale: (l: Locale) => void;
  t: Dictionary;
}

const I18nContext = createContext<I18nValue>({ locale: "en", setLocale: () => {}, t: en });

// detectLocale prefers a stored choice, then the browser's language.
function detectLocale(): Locale {
  if (typeof window === "undefined") return "en";
  const stored = window.localStorage.getItem(STORAGE_KEY);
  if (stored && stored in LOCALES) return stored as Locale;
  const browser = window.navigator.language.slice(0, 2).toLowerCase();
  return browser in LOCALES ? (browser as Locale) : "en";
}

export function I18nProvider({ children }: { children: React.ReactNode }) {
  // Always render English first so server and client markup match; the
  // stored locale is applied after mount.
  const [locale, setLocaleState] = useState<Locale>("en");

  useEffect(() => {
    const detected = detectLocale();
    if (detected !== "en") setLocaleState(detected);
    document.documentElement.lang = detected;
  }, []);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    window.localStorage.setItem(STORAGE_KEY, next);
    document.documentElement.lang = next;
  }, []);

  return (
    <I18nContext.Provider value={{ locale, setLocale, t: LOCALES[locale].dict }}>
      {children}
    </I18nContext.Provider>
  );
}

// useT returns the active dictionary: `const t = useT(); t.common.save`.
export function useT(): Dictionary {
  return useContext(I18nContext).t;
}

export function useLocale() {
  const { locale, setLocale } = useContext(I18nContext);
  return { locale, setLocale };
}
