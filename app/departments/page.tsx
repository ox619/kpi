"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogBody, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Badge } from "@/components/ui/badge"
import { Building2, ChevronDown, ChevronRight, Edit, Plus, Search, Trash2 } from "lucide-react"
import { departmentApi, type Department } from "@/lib/api"
import { useAppContext } from "@/lib/app-context"
import { LoadingInline } from "@/components/loading"
import { collectDepartmentIds, departmentIndent, flattenDepartmentTree } from "@/lib/department-tree"

export default function DepartmentsPage() {
  const { Alert, Confirm } = useAppContext()
  const [departments, setDepartments] = useState<Department[]>([])
  const [loading, setLoading] = useState(true)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingDepartment, setEditingDepartment] = useState<Department | null>(null)
  const [collapsed, setCollapsed] = useState<Set<number>>(new Set())
  const [search, setSearch] = useState("")
  const [formData, setFormData] = useState({
    name: "",
    description: "",
    parent_id: "0",
  })

  // 获取部门树
  const fetchDepartments = useCallback(async () => {
    try {
      setLoading(true)
      const response = await departmentApi.getTree()
      setDepartments(response.data || [])
    } catch (error) {
      console.error("获取部门树失败:", error)
      await Alert("错误", "获取部门树失败")
      setDepartments([])
    } finally {
      setLoading(false)
    }
  }, [Alert])

  useEffect(() => {
    fetchDepartments()
  }, [fetchDepartments])

  // 摊平后的部门（带层级与完整路径）
  const flatDepartments = useMemo(() => flattenDepartmentTree(departments), [departments])

  // 列表渲染行：搜索时平铺显示匹配项，否则按树的展开状态层级显示
  const rows = useMemo(() => {
    const keyword = search.trim().toLowerCase()

    if (keyword) {
      return flatDepartments
        .filter(option => option.path.toLowerCase().includes(keyword))
        .map(option => ({ dept: option.dept, depth: 0, label: option.path }))
    }

    const result: Array<{ dept: Department; depth: number; label: string }> = []
    const walk = (nodes: Department[], depth: number) => {
      for (const node of nodes) {
        result.push({ dept: node, depth, label: node.name })
        if (node.children && node.children.length > 0 && !collapsed.has(node.id)) {
          walk(node.children, depth + 1)
        }
      }
    }
    walk(departments, 0)
    return result
  }, [departments, flatDepartments, collapsed, search])

  // 上级部门选项：编辑时排除自己及所有子部门，避免把部门挂到自己的子树下
  const parentOptions = useMemo(() => {
    if (!editingDepartment) return flatDepartments
    const excluded = new Set(collectDepartmentIds(editingDepartment))
    return flatDepartments.filter(option => !excluded.has(option.dept.id))
  }, [flatDepartments, editingDepartment])

  const toggleCollapse = (id: number) => {
    setCollapsed(prev => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  // 创建或更新部门
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const payload = {
        name: formData.name.trim(),
        description: formData.description,
        parent_id: parseInt(formData.parent_id) || 0,
      }

      if (editingDepartment) {
        await departmentApi.update(editingDepartment.id, payload)
      } else {
        await departmentApi.create(payload)
      }

      await fetchDepartments()
      setDialogOpen(false)
      setEditingDepartment(null)
      setFormData({ name: "", description: "", parent_id: "0" })
    } catch (error) {
      console.error("保存部门失败:", error)
      await Alert("保存失败", "保存部门失败，请检查部门名称是否重复")
    }
  }

  // 删除部门
  const handleDelete = async (id: number) => {
    const result = await Confirm("删除部门", "确定要删除这个部门吗？")
    if (!result) return

    try {
      await departmentApi.delete(id)
      await fetchDepartments()
    } catch (error) {
      console.error("删除部门失败:", error)
      await Alert("删除失败", "删除部门失败，请确认该部门下没有子部门和在职员工")
    }
  }

  // 打开编辑对话框
  const handleEdit = (department: Department) => {
    setEditingDepartment(department)
    setFormData({
      name: department.name,
      description: department.description || "",
      parent_id: (department.parent_id || 0).toString(),
    })
    setDialogOpen(true)
  }

  // 打开新增对话框
  const handleAdd = () => {
    setEditingDepartment(null)
    setFormData({ name: "", description: "", parent_id: "0" })
    setDialogOpen(true)
  }

  // 新增子部门
  const handleAddChild = (department: Department) => {
    setEditingDepartment(null)
    setFormData({ name: "", description: "", parent_id: department.id.toString() })
    setDialogOpen(true)
  }

  return (
    <div className="space-y-6">
      {/* 响应式头部 */}
      <div className="flex justify-between items-center flex-wrap gap-4">
        <div>
          <h1 className="text-2xl sm:text-3xl font-bold text-foreground">部门管理</h1>
          <p className="text-muted-foreground mt-1 sm:mt-2">管理组织架构，部门层级与企业微信保持一致</p>
        </div>
        <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
          <DialogTrigger asChild>
            <Button onClick={handleAdd} className="w-full sm:w-auto lg:mt-8">
              <Plus className="w-4 h-4 mr-2" />
              添加部门
            </Button>
          </DialogTrigger>
          <DialogContent className="w-[95vw] sm:max-w-md mx-auto">
            <DialogHeader>
              <DialogTitle>{editingDepartment ? "编辑部门" : "添加部门"}</DialogTitle>
            </DialogHeader>
            <DialogBody>
              <form id="department-form" onSubmit={handleSubmit} className="space-y-4">
                <div className="flex flex-col gap-2">
                  <Label htmlFor="parent_id">上级部门</Label>
                  <Select
                    value={formData.parent_id}
                    onValueChange={value => setFormData({ ...formData, parent_id: value })}
                  >
                    <SelectTrigger>
                      <SelectValue placeholder="选择上级部门" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="0">无（顶级部门）</SelectItem>
                      {parentOptions.map(option => (
                        <SelectItem key={option.dept.id} value={option.dept.id.toString()}>
                          {departmentIndent(option.depth) + option.dept.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex flex-col gap-2">
                  <Label htmlFor="name">部门名称</Label>
                  <Input
                    id="name"
                    value={formData.name}
                    onChange={e => setFormData({ ...formData, name: e.target.value })}
                    required
                  />
                </div>
                <div className="flex flex-col gap-2">
                  <Label htmlFor="description">部门描述</Label>
                  <Input
                    id="description"
                    value={formData.description}
                    onChange={e => setFormData({ ...formData, description: e.target.value })}
                  />
                </div>
              </form>
            </DialogBody>
            <DialogFooter className="flex-col-reverse sm:flex-row sm:justify-end gap-2 sm:space-x-2 sm:gap-0">
              <Button
                type="button"
                variant="outline"
                onClick={() => setDialogOpen(false)}
                className="w-full sm:w-auto"
              >
                取消
              </Button>
              <Button type="submit" form="department-form" className="w-full sm:w-auto">
                {editingDepartment ? "更新" : "创建"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex flex-wrap items-center justify-between gap-4">
            <div className="flex items-center">
              <Building2 className="w-5 h-5 mr-2" />
              组织架构
            </div>
            <div className="flex items-center gap-4">
              <div className="relative">
                <Search className="w-4 h-4 absolute left-3 top-1/2 transform -translate-y-1/2 text-muted-foreground" />
                <Input
                  placeholder="搜索部门..."
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                  className="pl-10 w-48 sm:w-64"
                />
              </div>
            </div>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? (
            <LoadingInline className="py-8" message="加载中..." />
          ) : rows.length === 0 ? (
            <div className="text-center py-8 text-gray-500">
              {search ? "没有匹配的部门" : "暂无部门数据"}
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>部门名称</TableHead>
                  <TableHead>描述</TableHead>
                  <TableHead>人数</TableHead>
                  <TableHead>创建时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map(({ dept, depth, label }) => {
                  const hasChildren = !!dept.children && dept.children.length > 0
                  const isCollapsed = collapsed.has(dept.id)

                  return (
                    <TableRow key={dept.id}>
                      <TableCell>
                        <div
                          className="flex items-center"
                          style={{ paddingLeft: depth * 20 }}
                        >
                          {hasChildren && !search ? (
                            <button
                              type="button"
                              onClick={() => toggleCollapse(dept.id)}
                              className="mr-1 p-0.5 rounded hover:bg-muted text-muted-foreground"
                              aria-label={isCollapsed ? "展开子部门" : "收起子部门"}
                            >
                              {isCollapsed ? (
                                <ChevronRight className="w-4 h-4" />
                              ) : (
                                <ChevronDown className="w-4 h-4" />
                              )}
                            </button>
                          ) : (
                            <span className="w-5 mr-1" />
                          )}
                          <span className="font-medium">{label}</span>
                        </div>
                      </TableCell>
                      <TableCell>{dept.description}</TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1 flex-wrap">
                          <Badge variant="secondary">{dept.employee_count || 0} 人</Badge>
                          {hasChildren && (
                            <Badge variant="outline">
                              含子部门 {dept.total_employee_count || 0} 人
                            </Badge>
                          )}
                        </div>
                      </TableCell>
                      <TableCell>{new Date(dept.created_at).toLocaleDateString()}</TableCell>
                      <TableCell className="text-right space-x-2">
                        <Button variant="outline" size="sm" onClick={() => handleAddChild(dept)} title="添加子部门">
                          <Plus className="w-4 h-4" />
                        </Button>
                        <Button variant="outline" size="sm" onClick={() => handleEdit(dept)}>
                          <Edit className="w-4 h-4" />
                        </Button>
                        <Button variant="outline" size="sm" onClick={() => handleDelete(dept.id)}>
                          <Trash2 className="w-4 h-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
