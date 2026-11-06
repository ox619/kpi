"use client"

import { useState, type FormEvent } from "react"
import { KeyRound, Loader2 } from "lucide-react"
import { toast } from "sonner"
import { authApi, employeeApi, getErrorMessage, type Employee } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"

function passwordError(password: string, confirmation: string) {
  if ([...password].length < 6 || new TextEncoder().encode(password).length > 72) {
    return "密码至少 6 个字符且不超过 72 字节"
  }
  return password === confirmation ? "" : "两次输入的新密码不一致"
}

export function ChangePasswordCard() {
  const [currentPassword, setCurrentPassword] = useState("")
  const [newPassword, setNewPassword] = useState("")
  const [confirmation, setConfirmation] = useState("")
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (saving) return
    const validation = passwordError(newPassword, confirmation)
    setError(validation)
    if (validation) return
    setSaving(true)
    try {
      await authApi.changePassword(currentPassword, newPassword)
      setCurrentPassword("")
      setNewPassword("")
      setConfirmation("")
      toast.success("密码修改成功")
    } catch (err) {
      setError(getErrorMessage(err, "修改密码失败，请重试"))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><KeyRound className="w-5 h-5" />修改密码</CardTitle>
      </CardHeader>
      <CardContent>
        <p className="mb-6 text-sm text-muted-foreground">
          修改 KPI 账号的邮箱登录密码。未设置本地密码或忘记密码时，请联系 HR 在员工管理中重置。
        </p>
        <form onSubmit={submit} className="max-w-md space-y-4">
          <div className="space-y-2">
            <Label htmlFor="current-password">当前密码</Label>
            <Input id="current-password" type="password" autoComplete="current-password"
              value={currentPassword} onChange={event => setCurrentPassword(event.target.value)} disabled={saving} required />
          </div>
          <div className="space-y-2">
            <Label htmlFor="new-password">新密码</Label>
            <Input id="new-password" type="password" autoComplete="new-password"
              value={newPassword} onChange={event => setNewPassword(event.target.value)} disabled={saving} required minLength={6} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="confirm-password">确认新密码</Label>
            <Input id="confirm-password" type="password" autoComplete="new-password"
              value={confirmation} onChange={event => setConfirmation(event.target.value)} disabled={saving} required minLength={6} />
          </div>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          <Button type="submit" disabled={saving}>
            {saving && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
            {saving ? "正在保存..." : "保存新密码"}
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}

export function ResetEmployeePasswordDialog({ employee, onClose }: { employee: Employee; onClose: () => void }) {
  const [newPassword, setNewPassword] = useState("")
  const [confirmation, setConfirmation] = useState("")
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (saving) return
    const validation = passwordError(newPassword, confirmation)
    setError(validation)
    if (validation) return
    setSaving(true)
    try {
      await employeeApi.resetPassword(employee.id, newPassword)
      setNewPassword("")
      setConfirmation("")
      toast.success("密码已重置，请将新密码告知该员工")
      onClose()
    } catch (err) {
      setError(getErrorMessage(err, "重置密码失败，请重试"))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={open => { if (!open && !saving) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>重置员工密码</DialogTitle>
          <DialogDescription>
            为 {employee.name}（{employee.email}）设置新的 KPI 登录密码，原密码将失效。请在重置后将新密码告知本人。
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="reset-password">新密码</Label>
            <Input id="reset-password" type="password" autoComplete="new-password"
              value={newPassword} onChange={event => setNewPassword(event.target.value)} disabled={saving} required minLength={6} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="reset-password-confirm">确认新密码</Label>
            <Input id="reset-password-confirm" type="password" autoComplete="new-password"
              value={confirmation} onChange={event => setConfirmation(event.target.value)} disabled={saving} required minLength={6} />
          </div>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saving} onClick={onClose}>取消</Button>
            <Button type="submit" disabled={saving}>
              {saving && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
              {saving ? "正在重置..." : "确认重置"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}