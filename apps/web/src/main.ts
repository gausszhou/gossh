import { createApp, h } from 'vue'
import { createRouter, createWebHashHistory, RouterView } from 'vue-router'
import App from './App.vue'
// 唯一全局样式入口(html/body 锁滚动、xterm 字体),构建时内联进 main.js
import './style/index.css'
import { applyTheme, currentTheme } from './utils/theme'

// gossh:单页应用(主机清单 + 页签区),统一走 '/'。
const router = createRouter({
    history: createWebHashHistory(),
    routes: [
        { path: '/', component: App },
        { path: '/:pathMatch(.*)*', redirect: '/' },
    ],
})

// mount 前应用持久化主题:<html data-theme=...> 驱动 CSS 变量,避免首帧闪烁
applyTheme(currentTheme())

createApp({ render: () => h(RouterView) }).use(router).mount('#app')