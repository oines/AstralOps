export type AppLanguage = "system" | "en" | "zh-CN";
export type ResolvedLanguage = "en" | "zh-CN";

export const LANGUAGE_OPTIONS = [
  { value: "system", labelKey: "settings.language.system" },
  { value: "en", labelKey: "settings.language.en" },
  { value: "zh-CN", labelKey: "settings.language.zhCN" },
] as const;

export function resolveAppLanguage(language: AppLanguage | string | undefined, systemLanguage = ""): ResolvedLanguage {
  if (language === "en" || language === "zh-CN") return language;
  return systemLanguage.toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

export const mobileResources = {
  en: {
    common: {
      appName: "AstralOps",
      settings: "Settings",
      retry: "Retry",
      send: "Send",
      terminal: "Terminal",
      transcript: "Transcript",
      navigator: "Navigator",
      empty: "Empty",
    },
    mobile: {
      hosts: "Devices",
      workspaces: "Workspaces",
      sessions: "Sessions",
      newWorkspace: "New workspace",
      noWorkspace: "No workspace",
      noSession: "No session",
      requestControl: "Request control",
      openTerminal: "Open terminal",
      composerPlaceholder: "Request follow-up changes",
      terminalInputPaused: "Terminal input paused",
      selectSession: "Select a session",
      controllerOnly: "Mobile is controller-only",
    },
    status: {
      local: "Local",
      lan: "LAN",
      relay: "Relay",
      offline: "Offline",
      connecting: "Connecting",
      reconnecting: "Reconnecting",
      failed: "Failed",
      needs_pairing: "Approval required",
      pending: "Pending",
      revoked: "Revoked",
      live: "Connected",
    },
    settings: {
      title: "Settings",
      account: "Account",
      relay: "Relay",
      language: {
        label: "Language",
        system: "System",
        en: "English",
        zhCN: "简体中文",
      },
    },
  },
  "zh-CN": {
    common: {
      appName: "AstralOps",
      settings: "设置",
      retry: "重试",
      send: "发送",
      terminal: "终端",
      transcript: "对话",
      navigator: "导航",
      empty: "暂无",
    },
    mobile: {
      hosts: "设备",
      workspaces: "工作区",
      sessions: "会话",
      newWorkspace: "新工作区",
      noWorkspace: "没有工作区",
      noSession: "没有会话",
      requestControl: "请求控制",
      openTerminal: "打开终端",
      composerPlaceholder: "要求后续变更",
      terminalInputPaused: "终端输入已暂停",
      selectSession: "选择一个会话",
      controllerOnly: "手机仅作为遥控器",
    },
    status: {
      local: "本机",
      lan: "LAN",
      relay: "中继",
      offline: "离线",
      connecting: "连接中",
      reconnecting: "重连中",
      failed: "失败",
      needs_pairing: "待授权",
      pending: "等待批准",
      revoked: "已撤销",
      live: "已连接",
    },
    settings: {
      title: "设置",
      account: "账号",
      relay: "中继",
      language: {
        label: "语言",
        system: "跟随系统",
        en: "English",
        zhCN: "简体中文",
      },
    },
  },
} as const;

export type MobileResources = typeof mobileResources;
