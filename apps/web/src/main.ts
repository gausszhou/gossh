import { createApp, h } from 'vue'
import { createRouter, createWebHashHistory, RouterView } from 'vue-router'
import App from './App.vue'
// 唯一全局样式入口(html/body 锁滚动、xterm 字体),构建时内联进 main.js
import './style/index.css'
import { applyTheme, currentPreference } from './utils/theme'

// gossh:单页应用(主机清单 + 页签区),统一走 '/'。
const router = createRouter({
    history: createWebHashHistory(),
    routes: [
        { path: '/', component: App },
        { path: '/:pathMatch(.*)*', redirect: '/' },
    ],
})

// mount 前应用持久化主题偏好:<html data-theme=...> 驱动 CSS 变量,避免首帧闪烁。
// 注意传"偏好"(可能是 system)而不是解析后的亮/暗——否则 system 会被
// 固化成当时生效的那一种,系统偏好变化后不再跟随。
applyTheme(currentPreference())

createApp({ render: () => h(RouterView) }).use(router).mount('#app')