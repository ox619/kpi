"use client"

import { useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { AlertCircle, Loader2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { authApi, getErrorMessage } from "@/lib/api"
import { useAuth } from "@/lib/auth-context"
import { toast } from "sonner"

export default function WeComCallbackPage() {
  const router = useRouter()
  const { loginWithSession } = useAuth()
  const [error, setError] = useState("")
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true

    const params = new URLSearchParams(window.location.search)
    const ticket = params.get("ticket")
    if (!ticket) {
      setError("登录链接无效，请重新发起企业微信登录。")
      return
    }

    authApi
      .exchangeWeComLogin(ticket)
      .then((response) => {
        loginWithSession(response.token, response.user)
        toast.success("登录成功")
        router.replace(response.user.role === "hr" ? "/" : "/evaluations")
      })
      .catch((err: unknown) => {
        setError(getErrorMessage(err, "登录链接已失效，请重新登录。"))
      })
  }, [loginWithSession, router])

  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-center">企业微信登录</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col items-center gap-4 py-6">
          {error ? (
            <>
              <AlertCircle className="w-10 h-10 text-destructive" />
              <p className="text-sm text-muted-foreground text-center">{error}</p>
              <Button className="w-full" onClick={() => router.replace("/auth/login")}>
                返回登录页
              </Button>
            </>
          ) : (
            <>
              <Loader2 className="w-8 h-8 animate-spin text-primary" />
              <p className="text-sm text-muted-foreground">正在完成登录...</p>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
