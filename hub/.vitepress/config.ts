import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'OhMyCine Hub',
  description: 'OhMyCine 插件市场 — 发现、安装和管理插件',
  themeConfig: {
    nav: [
      { text: '首页', link: '/' },
      { text: '插件', link: '/plugins/' },
      { text: '开发', link: '/dev/' },
      { text: '文档', link: 'https://github.com/yuanjing-hash/OhMyCine' },
    ],
    sidebar: {
      '/plugins/': [
        {
          text: '插件市场',
          items: [
            { text: '浏览插件', link: '/plugins/' },
            { text: '安装指南', link: '/plugins/install' },
          ],
        },
      ],
      '/dev/': [
        {
          text: '插件开发',
          items: [
            { text: '快速开始', link: '/dev/' },
            { text: '插件规范', link: '/dev/spec' },
            { text: 'API 参考', link: '/dev/api' },
          ],
        },
      ],
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/yuanjing-hash/OhMyCine' },
    ],
    footer: {
      message: 'GPL-3.0 Licensed',
      copyright: 'OhMyCine',
    },
  },
})
