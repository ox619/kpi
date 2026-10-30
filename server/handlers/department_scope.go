package handlers

import (
	"strconv"

	"dootask-kpi-server/models"

	"gorm.io/gorm"
)

// departmentSubtreeIDs 返回部门及其所有子孙部门 ID。
// 查库失败时退化为只包含自身，保证查询仍能返回结果而不是直接报错。
func departmentSubtreeIDs(departmentID uint) []uint {
	ids, err := models.CollectDepartmentSubtreeIDs(departmentID)
	if err != nil || len(ids) == 0 {
		return []uint{departmentID}
	}
	return ids
}

// departmentSubtreeIDsFromParam 解析查询参数形式的部门 ID，返回其子树 ID。
func departmentSubtreeIDsFromParam(departmentID string) []uint {
	deptID, err := strconv.ParseUint(departmentID, 10, 32)
	if err != nil {
		return nil
	}
	return departmentSubtreeIDs(uint(deptID))
}

// applyDepartmentSubtreeScope 给查询追加「该部门及其所有子部门」的过滤条件。
// column 需要带上表名（如 employees.department_id）以避免 join 时歧义。
func applyDepartmentSubtreeScope(query *gorm.DB, column string, departmentID string) *gorm.DB {
	deptID, err := strconv.ParseUint(departmentID, 10, 32)
	if err != nil {
		return query.Where(column+" = ?", departmentID)
	}
	return query.Where(column+" IN ?", departmentSubtreeIDs(uint(deptID)))
}
