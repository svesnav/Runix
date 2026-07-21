import type { NextConfig } from "next";

// The control plane ships as one binary, so the UI is exported to plain
// files and embedded into it (see internal/webui). That rules out every
// server-side Next feature — which costs nothing here, because the app is
// already entirely client-rendered and talks to the API over fetch.
const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: "export",
  images: { unoptimized: true },
  // Exported as directories with an index.html, so a plain file server can
  // resolve /servers the same way it resolves /servers/.
  trailingSlash: true,
};

export default nextConfig;
