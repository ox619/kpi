import type { Department } from "@/lib/api"

export interface DepartmentOption {
  dept: Department
  depth: number
  path: string
}

// 把部门树摊平成「带层级」的选项列表，供下拉框渲染缩进使用
export function flattenDepartmentTree(
  nodes: Department[],
  depth = 0,
  parentPath = ""
): DepartmentOption[] {
  const result: DepartmentOption[] = []

  for (const node of nodes) {
    const path = parentPath ? `${parentPath}/${node.name}` : node.name
    result.push({ dept: node, depth, path })

    if (node.children && node.children.length > 0) {
      result.push(...flattenDepartmentTree(node.children, depth + 1, path))
    }
  }

  return result
}

// 把扁平列表（靠 parent_id 关联）还原成树后再摊平，
// 兼容只返回扁平结构的接口（例如注册页的公开部门接口）
export function flattenDepartmentList(list: Department[]): DepartmentOption[] {
  const existingIds = new Set(list.map(dept => dept.id))
  const byParent = new Map<number, Department[]>()

  for (const dept of list) {
    const parentId = dept.parent_id && existingIds.has(dept.parent_id) ? dept.parent_id : 0
    const siblings = byParent.get(parentId) ?? []
    siblings.push(dept)
    byParent.set(parentId, siblings)
  }

  const walk = (parentId: number, depth: number, parentPath: string): DepartmentOption[] => {
    const result: DepartmentOption[] = []
    for (const node of byParent.get(parentId) ?? []) {
      const path = parentPath ? `${parentPath}/${node.name}` : node.name
      result.push({ dept: node, depth, path })
      result.push(...walk(node.id, depth + 1, path))
    }
    return result
  }

  return walk(0, 0, "")
}

// 收集某个部门及其所有子孙部门的 ID（用于编辑时避免把部门挂到自己的子树下）
export function collectDepartmentIds(node: Department): number[] {
  const ids = [node.id]
  for (const child of node.children ?? []) {
    ids.push(...collectDepartmentIds(child))
  }
  return ids
}

// 下拉选项的缩进前缀（用全角空格，避免被 HTML 折叠）
export function departmentIndent(depth: number): string {
  return "　".repeat(depth)
}
