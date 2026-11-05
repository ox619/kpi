import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  /* config options here */
  output: process.env.NEXT_OUTPUT_MODE === "standalone" ? "standalone" : undefined,
  basePath: process.env.NEXT_PUBLIC_BASE_PATH || '',
  // 将 /api/* 代理到后端服务，保证企微 OAuth 回调（回跳走 3000 进 Next）能转到后端。
  async rewrites() {
    const apiPort = process.env.NEXT_PUBLIC_API_PORT || "8080";
    const apiHost = process.env.NEXT_PUBLIC_API_HOST || "127.0.0.1";
    return [
      {
        source: "/api/:path*",
        destination: `http://${apiHost}:${apiPort}/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
