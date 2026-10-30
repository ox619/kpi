package models

import "strings"

// DepartmentFullNameMap 返回「部门ID → 完整路径名」（如 总公司/技术部/后端组）。
// 扁平展示场景（统计报表、图表）用它区分不同上级下的同名部门。
func DepartmentFullNameMap() (map[uint]string, error) {
	type deptRow struct {
		ID       uint
		ParentID uint
		Name     string
	}

	var rows []deptRow
	if err := DB.Model(&Department{}).Select("id, parent_id, name").Find(&rows).Error; err != nil {
		return nil, err
	}

	byID := make(map[uint]deptRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	result := make(map[uint]string, len(rows))
	for _, r := range rows {
		parts := []string{r.Name}
		current := r
		seen := map[uint]bool{r.ID: true}
		for {
			parent, ok := byID[current.ParentID]
			if !ok || seen[parent.ID] {
				break
			}
			seen[parent.ID] = true
			parts = append([]string{parent.Name}, parts...)
			current = parent
		}
		result[r.ID] = strings.Join(parts, "/")
	}

	return result, nil
}

// DepartmentSubtreeMap 一次性计算「每个部门 → 它自己 + 所有子孙部门」的 ID 集合。
// 组织规模有限，一次全量遍历比在循环里反复查库更划算，也避免 N+1 查询。
// 带环保护：即使数据异常（父子成环）也不会死循环。
func DepartmentSubtreeMap() (map[uint][]uint, error) {
	type deptRow struct {
		ID       uint
		ParentID uint
	}

	var rows []deptRow
	if err := DB.Model(&Department{}).Select("id, parent_id").Find(&rows).Error; err != nil {
		return nil, err
	}

	childrenOf := make(map[uint][]uint, len(rows))
	for _, r := range rows {
		childrenOf[r.ParentID] = append(childrenOf[r.ParentID], r.ID)
	}

	result := make(map[uint][]uint, len(rows))
	for _, r := range rows {
		seen := map[uint]bool{r.ID: true}
		ids := []uint{r.ID}
		for i := 0; i < len(ids); i++ {
			for _, childID := range childrenOf[ids[i]] {
				if seen[childID] {
					continue
				}
				seen[childID] = true
				ids = append(ids, childID)
			}
		}
		result[r.ID] = ids
	}

	return result, nil
}

// CollectDepartmentSubtreeIDs 返回指定部门及其所有子孙部门的 ID（含自身）。
// 用于「按部门筛选/统计」时把子部门一并纳入。
func CollectDepartmentSubtreeIDs(rootID uint) ([]uint, error) {
	subtreeMap, err := DepartmentSubtreeMap()
	if err != nil {
		return nil, err
	}
	if ids, ok := subtreeMap[rootID]; ok {
		return ids, nil
	}
	return []uint{rootID}, nil
}
