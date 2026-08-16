/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  transpilePackages: ['@tremor/react', 'echarts-for-react', 'react-markdown', 'remark-gfm'],
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
