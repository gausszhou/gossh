// 轻量国际化:跟随浏览器语言(zh/en),设置弹窗内可手动切换并持久化。
// 不引入 vue-i18n 依赖:一个 reactive lang ref + 字典即可满足界面文案。
// 找不到翻译键时 t() 原样返回 key —— 界面文案因此始终可读。
import { ref } from 'vue'

export type Lang = 'zh' | 'en'

const LANG_KEY = 'gossh.lang'

const messages: Record<Lang, Record<string, string>> = {
    zh: {
        // ── 通用 ──
        'common.cancel': '取消',
        'common.close': '关闭',
        'common.save': '保存',
        'common.delete': '删除',
        'common.confirm': '确认',
        'common.loading': '加载中…',
        'common.error': '错误',
        // ── 页签栏 ──
        'tab.close': '关闭页签',
        'tab.settings': '打开设置',
        'tab.latency': '往返延迟(RTT),每 2 秒刷新',
        'tab.newHost': '新建主机',
        'tab.dragHint': '按住拖拽调整页签顺序',
        // ── 访问令牌门禁 ──
        'token.title': '需要访问令牌',
        'token.hint': 'gossh serve 启动时会打印带令牌的完整 URL(如 http://127.0.0.1:8040/?token=xxx),把 token= 之后的部分粘贴到这里;令牌只保存在本会话的 sessionStorage。',
        'token.placeholder': '粘贴访问令牌…',
        'token.submit': '进入',
        'token.invalid': '令牌不能为空或被拒绝,请核对服务端打印的 URL。',
        // ── 主机列表 ──
        'host.search': '搜索名称 / 地址 / 用户…',
        'host.empty': '暂无主机',
        'host.emptyHint': '点击右上角「新建主机」添加第一条记录',
        'host.ungrouped': '未分组',
        'host.collapse': '折叠主机列表',
        'host.expand': '展开主机列表',
        'host.act.connect': '连接',
        'host.act.sftp': 'SFTP',
        'host.act.forwards': '转发',
        'host.act.edit': '编辑',
        'host.act.delete': '删除',
        'host.act.confirmDelete': '确认删除?',
        'host.deleteFailed': '删除失败',
        'host.credDefault': 'default',
        'host.credKey': 'key',
        'host.credAgent': 'agent',
        'host.credPassword': 'password',
        // ── 主机表单 ──
        'hostForm.create': '新建主机',
        'hostForm.edit': '编辑主机',
        'hostForm.name': '名称',
        'hostForm.address': '地址',
        'hostForm.port': '端口',
        'hostForm.user': '用户名',
        'hostForm.group': '分组',
        'hostForm.credentialKind': '认证方式',
        'hostForm.credentialKeyPath': '私钥路径',
        'hostForm.password': '密码',
        'hostForm.passwordPlaceholder': '连接用密码(不写入主机清单)',
        'hostForm.savePassword': '保存到系统钥匙串',
        'hostForm.required': '名称、地址与用户名必填',
        'hostForm.portInvalid': '端口需为 1–65535 的整数',
        'hostForm.saveFailed': '保存失败',
        // ── 凭据弹窗 ──
        'cred.title': '需要凭据',
        'cred.message': '连接 %s 需要密码或私钥口令',
        'cred.retryMessage': '凭据无效,重新输入密码或私钥口令?',
        'cred.password': '密码',
        'cred.passphrase': '私钥口令',
        'cred.saveToKeyring': '保存到系统钥匙串',
        'cred.submit': '连接',
        'cred.failed': '连接失败',
        'cred.busy': '正在连接…',
        // ── 页签内容 ──
        'empty.title': '打开一个主机会话',
        'empty.hint': '从左侧主机列表选择主机,点「连接」打开会话',
        'empty.loading': '正在连接…',
        'dialog.gone': '会话已销毁',
        'dialog.lost': '连接已断开',
        'dialog.goneMsg': '该会话已被销毁或不存在',
        'dialog.reconnect': '重新连接',
        'dialog.close': '关闭',
        'ssh.forward': '端口转发',
        // ── SFTP ──
        'sftp.upload': '上传',
        'sftp.mkdir': '新建目录',
        'sftp.rename': '重命名',
        'sftp.delete': '删除',
        'sftp.download': '下载',
        'sftp.up': '上层目录',
        'sftp.home': '家目录',
        'sftp.refresh': '刷新',
        'sftp.expand': '展开文件列表',
        'sftp.collapse': '收起文件列表',
        'sftp.name': '名称',
        'sftp.size': '大小',
        'sftp.modTime': '修改时间',
        'sftp.type': '类型',
        'sftp.typeDir': '目录',
        'sftp.typeLink': '链接',
        'sftp.typeFile': '文件',
        'sftp.empty': '目录为空',
        'sftp.loadFailed': '加载失败',
        'sftp.mkdirPrompt': '目录名称:',
        'sftp.renamePrompt': '重命名为:',
        'sftp.deleteConfirm': '确认删除 %s?',
        'sftp.selectHint': '先选择一个文件 / 目录',
        'sftp.opFailed': '操作失败',
        'sftp.uploading': '正在上传 %s…',
        'sftp.uploaded': '已上传',
        'sftp.deleteDirNotEmpty': '目录非空,无法删除',
        'sftp.tabSuffix': 'SFTP',
        // ── 端口转发 ──
        'fwd.title': '端口转发',
        'fwd.kind': '类型',
        'fwd.local': '本地 (-L)',
        'fwd.remote': '远程 (-R)',
        'fwd.dynamic': '动态 (-D)',
        'fwd.bind': '绑定',
        'fwd.bindPlaceholder': '如 127.0.0.1:8080 或 8080',
        'fwd.target': '目标',
        'fwd.targetPlaceholder': '如 localhost:80(dynamic 可空)',
        'fwd.add': '添加',
        'fwd.empty': '暂无转发',
        'fwd.listTitle': '当前转发',
        'fwd.addFailed': '添加失败',
        'fwd.required': '填写绑定地址(以及目标)',
        // ── 主机级端口转发(持久定义,连上即生效) ──
        'hostForwards.title': '主机端口转发',
        'hostForwards.listTitle': '连接时自动应用',
        'hostForwards.hint': '保存后,每次连接该主机自动生效;单个转发失败不阻断连接。',
        'hostForwards.saveFailed': '保存失败',
        // ── 设置 ──
        'settings.open': '打开设置',
        'settings.title': '设置',
        'settings.theme': '主题',
        'settings.dark': '深色',
        'settings.light': '浅色',
        'settings.language': '语言',
        'settings.token': '访问令牌',
        'settings.tokenHint': '来自页面 URL ?token=,已存入本会话的 sessionStorage',
        'settings.knownHosts': '已知主机密钥',
        'settings.knownHostsEmpty': '暂无记录(首次连接时自动信任)',
        'settings.forget': '删除',
        'settings.forgetConfirm': '删除该主机的密钥信任?',
        'settings.forgetFailed': '删除失败',
        'settings.pageTitle': '页面标题',
        'settings.pageTitlePlaceholder': '浏览器标签页标题,留空恢复默认',
        'settings.save': '保存',
        'settings.saved': '已保存',
        'settings.saveFailed': '保存失败',
    },
    en: {
        // ── Generic ──
        'common.cancel': 'Cancel',
        'common.close': 'Close',
        'common.save': 'Save',
        'common.delete': 'Delete',
        'common.confirm': 'Confirm',
        'common.loading': 'Loading…',
        'common.error': 'Error',
        // ── Tab bar ──
        'tab.close': 'Close tab',
        'tab.settings': 'Open settings',
        'tab.latency': 'Round-trip latency (RTT), refreshed every 2s',
        'tab.newHost': 'New host',
        'tab.dragHint': 'Drag to reorder tabs',
        // ── Access token gate ──
        'token.title': 'Access token required',
        'token.hint': 'gossh serve prints a token URL on startup (e.g. http://127.0.0.1:8040/?token=xxx); paste the part after token= here. The token lives only in this session\'s sessionStorage.',
        'token.placeholder': 'Paste the access token…',
        'token.submit': 'Enter',
        'token.invalid': 'Token cannot be empty or was rejected; check the URL printed by the server.',
        // ── Host list ──
        'host.search': 'Search name / address / user…',
        'host.empty': 'No hosts yet',
        'host.emptyHint': 'Click "New host" in the top-right to add one',
        'host.ungrouped': 'Ungrouped',
        'host.collapse': 'Collapse host list',
        'host.expand': 'Expand host list',
        'host.act.connect': 'Connect',
        'host.act.sftp': 'SFTP',
        'host.act.forwards': 'Forwards',
        'host.act.edit': 'Edit',
        'host.act.delete': 'Delete',
        'host.act.confirmDelete': 'Confirm?',
        'host.deleteFailed': 'Failed to delete',
        'host.credDefault': 'default',
        'host.credKey': 'key',
        'host.credAgent': 'agent',
        'host.credPassword': 'password',
        // ── Host form ──
        'hostForm.create': 'New host',
        'hostForm.edit': 'Edit host',
        'hostForm.name': 'Name',
        'hostForm.address': 'Address',
        'hostForm.port': 'Port',
        'hostForm.user': 'User',
        'hostForm.group': 'Group',
        'hostForm.credentialKind': 'Credential',
        'hostForm.credentialKeyPath': 'Key path',
        'hostForm.password': 'Password',
        'hostForm.passwordPlaceholder': 'Password for connecting (not stored in the inventory)',
        'hostForm.savePassword': 'Save to system keyring',
        'hostForm.required': 'Name, address and user are required',
        'hostForm.portInvalid': 'Port must be an integer 1–65535',
        'hostForm.saveFailed': 'Failed to save',
        // ── Credentials modal ──
        'cred.title': 'Credentials required',
        'cred.message': 'Connecting to %s requires a password or key passphrase',
        'cred.retryMessage': 'Invalid credentials — enter the password or key passphrase again?',
        'cred.password': 'Password',
        'cred.passphrase': 'Key passphrase',
        'cred.saveToKeyring': 'Save to system keyring',
        'cred.submit': 'Connect',
        'cred.failed': 'Connection failed',
        'cred.busy': 'Connecting…',
        // ── Tab content ──
        'empty.title': 'Open a host session',
        'empty.hint': 'Pick a host on the left and click Connect',
        'empty.loading': 'Connecting…',
        'dialog.gone': 'Session closed',
        'dialog.lost': 'Connection lost',
        'dialog.goneMsg': 'This session has been destroyed or does not exist',
        'dialog.reconnect': 'Reconnect',
        'dialog.close': 'Close',
        'ssh.forward': 'Port forwards',
        // ── SFTP ──
        'sftp.upload': 'Upload',
        'sftp.mkdir': 'New folder',
        'sftp.rename': 'Rename',
        'sftp.delete': 'Delete',
        'sftp.download': 'Download',
        'sftp.up': 'Up',
        'sftp.home': 'Home',
        'sftp.refresh': 'Refresh',
        'sftp.expand': 'Expand file list',
        'sftp.collapse': 'Collapse file list',
        'sftp.name': 'Name',
        'sftp.size': 'Size',
        'sftp.modTime': 'Modified',
        'sftp.type': 'Type',
        'sftp.typeDir': 'Directory',
        'sftp.typeLink': 'Link',
        'sftp.typeFile': 'File',
        'sftp.empty': 'Empty directory',
        'sftp.loadFailed': 'Failed to load',
        'sftp.mkdirPrompt': 'Folder name:',
        'sftp.renamePrompt': 'Rename to:',
        'sftp.deleteConfirm': 'Delete %s?',
        'sftp.selectHint': 'Select a file / folder first',
        'sftp.opFailed': 'Operation failed',
        'sftp.uploading': 'Uploading %s…',
        'sftp.uploaded': 'Uploaded',
        'sftp.deleteDirNotEmpty': 'Directory not empty, cannot delete',
        'sftp.tabSuffix': 'SFTP',
        // ── Port forwards ──
        'fwd.title': 'Port forwards',
        'fwd.kind': 'Type',
        'fwd.local': 'Local (-L)',
        'fwd.remote': 'Remote (-R)',
        'fwd.dynamic': 'Dynamic (-D)',
        'fwd.bind': 'Bind',
        'fwd.bindPlaceholder': 'e.g. 127.0.0.1:8080 or 8080',
        'fwd.target': 'Target',
        'fwd.targetPlaceholder': 'e.g. localhost:80 (blank for dynamic)',
        'fwd.add': 'Add',
        'fwd.empty': 'No forwards',
        'fwd.listTitle': 'Active forwards',
        'fwd.addFailed': 'Failed to add',
        'fwd.required': 'Fill in the bind address (and target)',
        // ── Host-level port forwards (persistent, applied on connect) ──
        'hostForwards.title': 'Host port forwards',
        'hostForwards.listTitle': 'Applied automatically on connect',
        'hostForwards.hint': 'Applied every time you connect to this host; a failing forward does not block the connection.',
        'hostForwards.saveFailed': 'Failed to save',
        // ── Settings ──
        'settings.open': 'Open settings',
        'settings.title': 'Settings',
        'settings.theme': 'Theme',
        'settings.dark': 'Dark',
        'settings.light': 'Light',
        'settings.language': 'Language',
        'settings.token': 'Access token',
        'settings.tokenHint': 'From the page URL ?token=, stored in this session\'s sessionStorage',
        'settings.knownHosts': 'Known host keys',
        'settings.knownHostsEmpty': 'No records yet (auto-trusted on first connect)',
        'settings.forget': 'Delete',
        'settings.forgetConfirm': 'Forget this host\'s key trust?',
        'settings.forgetFailed': 'Failed to delete',
        'settings.pageTitle': 'Page title',
        'settings.pageTitlePlaceholder': 'Browser tab title; empty restores default',
        'settings.save': 'Save',
        'settings.saved': 'Saved',
        'settings.saveFailed': 'Failed to save',
    },
}

function detectLang(): Lang {
    return (navigator.language || '').toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

function loadLang(): Lang {
    try {
        const v = localStorage.getItem(LANG_KEY)
        if (v === 'zh' || v === 'en') return v
    } catch {
        // localStorage 不可用时静默降级
    }
    return detectLang()
}

// lang 为全局响应式状态:切换后所有使用 t() 的模板自动重渲染。
export const lang = ref<Lang>(loadLang())

// 初始化同步 <html lang>。
document.documentElement.lang = lang.value

// t 返回当前语言文案;未知 key 原样返回(便于发现遗漏)。
export function t(key: string): string {
    return messages[lang.value][key] ?? key
}

// setLang 切换语言并持久化。
export function setLang(l: Lang) {
    lang.value = l
    document.documentElement.lang = l
    try {
        localStorage.setItem(LANG_KEY, l)
    } catch {
        // 忽略持久化失败
    }
}

// toggleLang 中/英互切,返回切换后的语言。
export function toggleLang(): Lang {
    setLang(lang.value === 'zh' ? 'en' : 'zh')
    return lang.value
}