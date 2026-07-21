"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useAuth, useAuthHydrated } from "@/stores/auth";

export default function Home() {
  const router = useRouter();
  const hydrated = useAuthHydrated();
  const refreshToken = useAuth((s) => s.refreshToken);
  useEffect(() => {
    if (hydrated) router.replace(refreshToken ? "/dashboard" : "/login");
  }, [router, hydrated, refreshToken]);
  return null;
}
