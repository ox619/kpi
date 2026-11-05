"use client"

import { useState, useEffect } from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"
import { Circle, CircleCheck } from "lucide-react"
import { RefreshCw, CheckCircle, LogOut, Monitor, Sun, Moon, Palette, Shield, Link2, Loader2 } from "lucide-react"
import { useAuth } from "@/lib/auth-context"
import { useAppContext } from "@/lib/app-context"
import { useTheme } from "@/lib/theme-context"
import { authApi, getErrorMessage, settingsApi } from "@/lib/api"
import { toast } from "sonner"
import { cn } from "@/lib/utils"

type SettingTab = "appearance" | "account" | "system" | "logout"

export default function SettingsPage() {
  const { logout, isHR } = useAuth()
  const { Confirm } = useAppContext()
  const { theme, setTheme } = useTheme()
  const [allowRegistration, setAllowRegistration] = useState(true)
  const [loading, setLoading] = useState(false)
  const [activeTab, setActiveTab] = useState<SettingTab>("appearance")
  const [binding, setBinding] = useState<{
    enabled: boolean
    bound: boolean
    wecom_user_id?: string
  } | null>(null)
  const [bindingStarting, setBindingStarting] = useState(false)
  const [unbindOpen, setUnbindOpen] = useState(false)
  const [unbindPassword, setUnbindPassword] = useState("")
  const [unbinding, setUnbinding] = useState(false)
  const [unbindError, setUnbindError] = useState("")

  // 初始化设置状态
  useEffect(() => {
    const fetchSettings = async () => {
      if (!isHR) return // 非HR用户不需要加载系统设置
      
      try {
        setLoading(true)
        const response = await settingsApi.get()
        setAllowRegistration(response.data.allow_registration)
      } catch (error) {
        console.error("获取设置失败:", error)
        toast.error("获取设置失败")
      } finally {
        setLoading(false)
      }
    }
    fetchSettings()
  }, [isHR])

  // 读取当前账号的企业微信绑定状态
  useEffect(() => {
    let cancelled = false
    authApi
      .getWeComBinding()
      .then((response) => {
        if (!cancelled) setBinding(response)
      })
      .catch((error) => {
        console.error("获取企业微信绑定状态失败:", error)
        if (!cancelled) setBinding({ enabled: false, bound: false })
      })
    return () => {
      cancelled = true
    }
  }, [])

  // 绑定回调会带 ?wecom=... 回到设置页，这里展示结果并清掉查询参数
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const result = params.get("wecom")
    if (!result) return

    if (result === "bound") {
      toast.success("企业微信绑定成功")
    } else if (result === "conflict") {
      toast.error("该企业微信身份已绑定到其他账号，请联系管理员")
    } else if (result === "browser") {
      toast.error("绑定校验失败，请使用与回调地址一致的域名，在同一浏览器中重新发起并完成绑定，且允许 Cookie")
    } else if (result === "disabled") {
      toast.error("企业微信账号已停用或未激活，无法绑定")
    } else {
      toast.error("企业微信绑定失败，请重试或联系管理员")
    }

    params.delete("wecom")
    const query = params.toString()
    window.history.replaceState({}, "", window.location.pathname + (query ? `?${query}` : ""))
  }, [])

  // 发起企业微信绑定：后端返回企微授权链接，浏览器直接跳转
  const handleStartBind = async () => {
    setBindingStarting(true)
    try {
      const response = await authApi.startWeComBind()
      if (!response?.url) {
        throw new Error("未获取到企业微信授权地址")
      }
      const redirectURI = new URL(response.url).searchParams.get("redirect_uri")
      if (!redirectURI) throw new Error("企业微信授权地址缺少回调地址，请联系管理员")
      const callback = new URL(redirectURI)
      if (callback.origin !== window.location.origin) {
        throw new Error("当前访问地址与企业微信回调地址不一致，请从 " + callback.origin + (process.env.NEXT_PUBLIC_BASE_PATH || "") + " 进入系统后重新绑定")
      }
      window.location.href = response.url
    } catch (error) {
      toast.error(getErrorMessage(error, "发起企业微信绑定失败"))
      setBindingStarting(false)
    }
  }

  const handleUnbind = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (unbinding) return
    setUnbinding(true)
    setUnbindError("")
    try {
      await authApi.unbindWeCom(unbindPassword)
      setBinding(previous => previous ? { enabled: previous.enabled, bound: false } : previous)
      setUnbindPassword("")
      setUnbindOpen(false)
      toast.success("企业微信已解绑，可使用邮箱密码登录或重新绑定")
    } catch (error) {
      setUnbindError(getErrorMessage(error, "解绑失败，请重试"))
    } finally {
      setUnbinding(false)
    }
  }

  // 保存设置
  const handleSaveSettings = async () => {
    if (!isHR) {
      toast.error("只有HR用户可以保存系统设置")
      return
    }

    setLoading(true)
    try {
      const response = await settingsApi.update({
        allow_registration: allowRegistration,
      })
      toast.success(response.message || "设置保存成功！")
    } catch (error) {
      console.error("保存设置失败:", error)
      toast.error("保存设置失败，请重试。")
    } finally {
      setLoading(false)
    }
  }

  // 退出登录
  const handleLogout = async () => {
    const result = await Confirm("退出登录", "确定要退出当前账户吗？")
    if (result) {
      logout()
    }
  }

  // 菜单项配置
  const menuItems = [
    {
      id: "appearance" as SettingTab,
      label: "外观设置",
      icon: <Palette className="w-4 h-4" />,
      available: true,
    },
    {
      id: "account" as SettingTab,
      label: "账号绑定",
      icon: <Link2 className="w-4 h-4" />,
      available: true,
    },
    {
      id: "system" as SettingTab,
      label: "系统设置",
      icon: <Shield className="w-4 h-4" />,
      available: isHR,
    },
    {
      id: "logout" as SettingTab,
      label: "退出登录",
      icon: <LogOut className="w-4 h-4" />,
      available: true,
      action: handleLogout,
    },
  ]

  // 渲染外观设置内容
  const renderAppearanceContent = () => (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center">
          <Palette className="w-5 h-5 mr-2" />
          外观设置
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        <div>
          <Label htmlFor="theme" className="text-sm font-medium mb-2 block">
            主题模式
          </Label>
          <div className="flex gap-4">
            {/* 浅色卡片 */}
            <button
              type="button"
              className={cn(
                "group flex-1 rounded-xl flex flex-col items-center p-0 overflow-hidden transition-all cursor-pointer shadow-sm hover:shadow-md",
                theme === "light"
                  ? "ring-1 ring-primary bg-background"
                  : "bg-background hover:bg-muted/50"
              )}
              onClick={() => setTheme("light")}
              aria-label="浅色模式"
            >
              {/* 预览区 */}
              <div className="w-full h-16 flex items-center justify-center bg-white border-b border-muted">
                <div className="w-8 h-8 rounded bg-background flex items-center justify-center">
                  <Sun className="w-5 h-5 text-yellow-400" />
                </div>
              </div>
              {/* 名称区 */}
              <div className="flex flex-col items-center py-3">
                <div className="flex items-center gap-1.5">
                  {theme === "light"
                    ? <CircleCheck className="w-4 h-4 text-primary" />
                    : <Circle className="w-4 h-4 text-muted-foreground" />}
                  <span className="font-medium text-sm text-foreground">浅色</span>
                </div>
              </div>
            </button>
            {/* 深色卡片 */}
            <button
              type="button"
              className={cn(
                "group flex-1 rounded-xl flex flex-col items-center p-0 overflow-hidden transition-all cursor-pointer shadow-sm hover:shadow-md",
                theme === "dark"
                  ? "ring-1 ring-primary bg-background"
                  : "bg-background hover:bg-muted/50"
              )}
              onClick={() => setTheme("dark")}
              aria-label="深色模式"
            >
              {/* 预览区 */}
              <div className="w-full h-16 flex items-center justify-center bg-zinc-900 border-b border-muted">
                <div className="w-8 h-8 rounded bg-zinc-800 flex items-center justify-center">
                  <Moon className="w-5 h-5 text-blue-400" />
                </div>
              </div>
              {/* 名称区 */}
              <div className="flex flex-col items-center py-3">
                <div className="flex items-center gap-1.5">
                  {theme === "dark"
                    ? <CircleCheck className="w-4 h-4 text-primary" />
                    : <Circle className="w-4 h-4 text-muted-foreground" />}
                  <span className="font-medium text-sm text-foreground">深色</span>
                </div>
              </div>
            </button>
            {/* 跟随系统卡片 */}
            <button
              type="button"
              className={cn(
                "group flex-1 rounded-xl flex flex-col items-center p-0 overflow-hidden transition-all cursor-pointer shadow-sm hover:shadow-md",
                theme === "system"
                  ? "ring-1 ring-primary bg-background"
                  : "bg-background hover:bg-muted/50"
              )}
              onClick={() => setTheme("system")}
              aria-label="跟随系统"
            >
              {/* 预览区 */}
              <div className="w-full h-16 flex items-center justify-center bg-gradient-to-r from-white via-zinc-900 to-white border-b border-muted">
                <div className="w-8 h-8 rounded bg-gradient-to-br from-white via-zinc-900 to-zinc-800 flex items-center justify-center">
                  <Monitor className="w-5 h-5 text-green-500" />
                </div>
              </div>
              {/* 名称区 */}
              <div className="flex flex-col items-center py-3 max-w-full overflow-x-auto">
                <div className="flex items-center gap-1.5 px-2">
                  {theme === "system"
                    ? <CircleCheck className="w-4 h-4 text-primary shrink-0" />
                    : <Circle className="w-4 h-4 text-muted-foreground shrink-0" />}
                  <span className="font-medium text-sm text-foreground">跟随系统</span>
                </div>
              </div>
            </button>
          </div>
        </div>
        <div className="p-4 bg-muted/50 rounded-lg">
          <h3 className="text-sm font-medium mb-2 text-foreground">主题说明</h3>
          <ul className="text-sm space-y-1 text-muted-foreground">
            <li>• 浅色模式：使用亮色背景和深色文字</li>
            <li>• 深色模式：使用深色背景和浅色文字，有助于减少眼部疲劳</li>
            <li>• 跟随系统：自动根据您的设备系统设置切换主题</li>
          </ul>
        </div>
      </CardContent>
    </Card>
  )

  // 渲染账号绑定内容
  const renderAccountContent = () => {
    const enabled = binding?.enabled ?? false
    const bound = binding?.bound ?? false

    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center">
            <Link2 className="w-5 h-5 mr-2" />
            账号绑定
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-6">
          <div className="flex flex-col gap-4 p-4 bg-muted/50 rounded-lg sm:flex-row sm:items-start sm:justify-between">
            <div className="flex-1">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">企业微信</span>
                {bound ? (
                  <span className="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-950/40 dark:text-green-300">
                    <CheckCircle className="w-3 h-3" />
                    已绑定
                  </span>
                ) : (
                  <span className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                    未绑定
                  </span>
                )}
              </div>
              <p className="text-sm text-muted-foreground mt-2 leading-relaxed">
                {bound
                  ? "当前账号已绑定企业微信身份：" + (binding?.wecom_user_id || "—") + "。" +
                    (enabled ? "绑定后可直接用企业微信登录。" : "企业微信接入当前未启用，仍可解绑。")
                  : !enabled
                    ? "企业微信接入未启用，请联系管理员配置 WECOM_* 环境变量。"
                    : "绑定后可以直接用企业微信登录本系统。绑定过程不需要再次输入密码。"}
              </p>
              {bound && (
                <p className="text-xs text-muted-foreground mt-2">
                  解绑需要当前 KPI 账号的密码；解绑后可重新绑定其他企业微信身份。
                </p>
              )}
            </div>
            {bound && (
              <Button variant="outline" onClick={() => {
                setUnbindPassword("")
                setUnbindError("")
                setUnbindOpen(true)
              }}>
                解绑企业微信
              </Button>
            )}
            {enabled && !bound && (
              <Button onClick={handleStartBind} disabled={bindingStarting || binding === null}>
                {bindingStarting ? (
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                ) : (
                  <Link2 className="w-4 h-4 mr-2" />
                )}
                绑定企业微信
              </Button>
            )}
          </div>

          {binding === null && (
            <p className="text-sm text-muted-foreground">正在读取绑定状态...</p>
          )}

          <div className="p-4 bg-muted/50 rounded-lg">
            <h3 className="text-sm font-medium mb-2 text-foreground">说明</h3>
            <ul className="text-sm space-y-1 text-muted-foreground">
              <li>• 企业微信登录只对已绑定的账号生效，未绑定时请先用邮箱密码登录。</li>
              <li>• 一个本地账号只能绑定一个企业微信身份，一个企业微信身份也只能绑定一个账号。</li>
              <li>• 请使用配置的回调地址进入系统，在同一浏览器中完成绑定，并允许 Cookie。</li>
              <li>• 解绑需验证当前 KPI 账号密码，不会退出当前登录。</li>
            </ul>
          </div>
        </CardContent>
      </Card>
    )
  }

  // 渲染系统设置内容
  const renderSystemContent = () => (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center">
          <Shield className="w-5 h-5 mr-2" />
          系统设置
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="flex items-center justify-between p-4 bg-muted/50 rounded-lg">
          <div className="flex-1">
            <Label htmlFor="allow_registration" className="text-sm font-medium">
              允许用户注册
            </Label>
            <p className="text-sm text-muted-foreground mt-1">
              开启后，新用户可以自行注册账户；关闭后，只能由管理员创建账户。
            </p>
          </div>
          <Switch id="allow_registration" checked={allowRegistration} onCheckedChange={setAllowRegistration} />
        </div>
        <div className="p-4 bg-muted/50 rounded-lg">
          <h3 className="text-sm font-medium mb-2 text-foreground">功能说明</h3>
          <ul className="text-sm space-y-1 text-muted-foreground">
            <li>• 开启注册：用户可以通过注册页面创建新账户</li>
            <li>• 关闭注册：注册页面将显示&quot;暂不开放注册&quot;的提示</li>
            <li>• 只有HR用户可以修改此设置</li>
          </ul>
        </div>
        <div className="flex justify-end">
          <Button onClick={handleSaveSettings} disabled={loading}>
            {loading ? <RefreshCw className="w-4 h-4 mr-2 animate-spin" /> : <CheckCircle className="w-4 h-4 mr-2" />}
            保存设置
          </Button>
        </div>
      </CardContent>
    </Card>
  )

  // 处理菜单项点击
  const handleMenuClick = (item: typeof menuItems[0]) => {
    if (item.action) {
      item.action()
    } else {
      setActiveTab(item.id)
    }
  }

  return (
    <div className="space-y-6">
      <Dialog open={unbindOpen} onOpenChange={open => {
        if (unbinding) return
        setUnbindOpen(open)
        setUnbindPassword("")
        setUnbindError("")
      }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>解绑企业微信</DialogTitle>
            <DialogDescription>
              解绑后将无法通过该企业微信登录本账号。请输入当前 KPI 账号的密码，确认您仍可使用邮箱密码登录。若未设置或忘记密码，请联系管理员。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={handleUnbind} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="wecom-unbind-password">当前账号密码</Label>
              <Input id="wecom-unbind-password" type="password" autoComplete="current-password"
                value={unbindPassword} onChange={event => setUnbindPassword(event.target.value)}
                disabled={unbinding} required aria-invalid={!!unbindError}
                aria-describedby={unbindError ? "wecom-unbind-error" : undefined} />
              {unbindError && <p id="wecom-unbind-error" role="alert" className="text-sm text-destructive">{unbindError}</p>}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={unbinding} onClick={() => {
                setUnbindOpen(false)
                setUnbindPassword("")
                setUnbindError("")
              }}>取消</Button>
              <Button type="submit" variant="destructive" disabled={unbinding || !unbindPassword}>
                {unbinding && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
                {unbinding ? "正在解绑..." : "确认解绑"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      {/* 页面标题 */}
      <div>
        <h1 className="text-3xl font-bold text-foreground">系统设置</h1>
        <p className="text-muted-foreground mt-2">管理您的个人偏好和系统配置</p>
      </div>

      {/* 主要内容区域 */}
      <div className="grid grid-cols-1 lg:grid-cols-4 gap-6">
        {/* 左侧导航菜单 - 小屏幕时显示在上方 */}
        <div className="lg:col-span-1">
          <Card className="sticky top-6">
            <CardHeader className="pb-3">
              <CardTitle className="text-lg">设置菜单</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              {menuItems
                .filter(item => item.available)
                .map((item) => (
                  <Button
                    key={item.id}
                    variant={activeTab === item.id ? "default" : "ghost"}
                    className={cn(
                      "w-full justify-start text-left",
                      item.id === "logout" && "text-destructive hover:text-destructive/90 hover:bg-destructive/10"
                    )}
                    onClick={() => handleMenuClick(item)}
                  >
                    {item.icon}
                    <span className="ml-2">{item.label}</span>
                  </Button>
                ))}
            </CardContent>
          </Card>
        </div>

        {/* 右侧内容区域 */}
        <div className="lg:col-span-3">
          {activeTab === "appearance" && renderAppearanceContent()}
          {activeTab === "account" && renderAccountContent()}
          {activeTab === "system" && isHR && renderSystemContent()}
          {activeTab === "system" && !isHR && (
            <div className="flex items-center justify-center h-64">
              <div className="text-center">
                <Shield className="w-12 h-12 mx-auto text-muted-foreground mb-4" />
                <h3 className="text-lg font-medium mb-2">权限不足</h3>
                <p className="text-muted-foreground">只有HR用户可以访问系统设置</p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
