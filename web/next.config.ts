import type { NextConfig } from "next";

const api = process.env.API_URL || "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  async rewrites() {
    return [
      { source: "/api/v1/:path*", destination: `${api}/api/v1/:path*` },
    ];
  },
};

export default nextConfig;
