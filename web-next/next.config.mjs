/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  transpilePackages: ['@tremor/react', 'echarts-for-react', 'react-markdown', 'remark-gfm'],
  // 按需导入: 避免一次性 import 几十个图标的 barrel 库(lucide/date-fns)在 dev 冷编译与 prod 打包时
  // 把整包拉进模块图, 显著减少编译模块数与首屏体积。
  experimental: {
    optimizePackageImports: ['lucide-react', 'date-fns', 'react-markdown', 'remark-gfm'],
  },
  async rewrites() {
    return [
      {
        source: '/api/:path*',
        // 本地直跑 go run 走 localhost:8080；容器内(docker compose)经 BACKEND_URL=core:8080 转发
        destination: `${process.env.BACKEND_URL || 'http://localhost:8080'}/api/:path*`,
      },
    ]
  },
}

export default nextConfig
