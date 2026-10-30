package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"dootask-kpi-server/models"

	"github.com/gin-gonic/gin"
)

// 获取所有部门（扁平列表，带分页；供下拉框等场景使用）
func GetDepartments(c *gin.Context) {
	var departments []models.Department

	// 解析分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	search := c.Query("search")

	// 验证分页参数
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	// 构建查询，只预加载在职员工
	query := models.DB.Preload("Employees", "is_active = ?", true)

	// 添加搜索条件
	if search != "" {
		searchPattern := "%" + search + "%"
		query = query.Where("name LIKE ? OR description LIKE ?",
			searchPattern, searchPattern)
	}

	// 获取总数
	var total int64
	countQuery := models.DB.Model(&models.Department{})
	if search != "" {
		searchPattern := "%" + search + "%"
		countQuery = countQuery.Where("name LIKE ? OR description LIKE ?",
			searchPattern, searchPattern)
	}

	if err := countQuery.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "获取部门总数失败",
			"message": err.Error(),
		})
		return
	}

	// 分页查询
	offset := (page - 1) * pageSize
	result := query.Order("parent_id asc, id asc").Offset(offset).Limit(pageSize).Find(&departments)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "获取部门列表失败",
			"message": result.Error.Error(),
		})
		return
	}

	// 计算分页信息
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	c.JSON(http.StatusOK, gin.H{
		"data":       departments,
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"totalPages": totalPages,
		"hasNext":    page < totalPages,
		"hasPrev":    page > 1,
	})
}

// 公开的部门列表（注册页使用）
// 只返回层级所需的最小字段，避免把员工信息暴露在未认证接口上。
func GetPublicDepartments(c *gin.Context) {
	type publicDepartment struct {
		ID       uint   `json:"id"`
		Name     string `json:"name"`
		ParentID uint   `json:"parent_id"`
	}

	var departments []publicDepartment
	if err := models.DB.Model(&models.Department{}).
		Select("id, name, parent_id").
		Order("parent_id asc, id asc").
		Find(&departments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "获取部门列表失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  departments,
		"total": len(departments),
	})
}

// 部门树节点（仅用于树形接口，不落库）
type departmentTreeNode struct {
	models.Department
	EmployeeCount      int64                 `json:"employee_count"`       // 直属在职人数
	TotalEmployeeCount int64                 `json:"total_employee_count"` // 含所有子部门
	Children           []*departmentTreeNode `json:"children,omitempty"`
}

// 获取完整部门树
// 与扁平列表不同，这里不 paginate：组织架构本身规模有限，一次返回便于前端渲染树。
func GetDepartmentTree(c *gin.Context) {
	var departments []models.Department
	if err := models.DB.Order("parent_id asc, id asc").Find(&departments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "获取部门树失败",
			"message": err.Error(),
		})
		return
	}

	// 各部门的直属在职人数
	type countRow struct {
		DepartmentID uint
		Cnt          int64
	}
	var counts []countRow
	models.DB.Model(&models.Employee{}).
		Select("department_id, count(*) as cnt").
		Where("is_active = ?", true).
		Group("department_id").
		Scan(&counts)

	directCount := make(map[uint]int64, len(counts))
	for _, row := range counts {
		directCount[row.DepartmentID] = row.Cnt
	}

	nodes := make(map[uint]*departmentTreeNode, len(departments))
	for i := range departments {
		dept := departments[i]
		nodes[dept.ID] = &departmentTreeNode{
			Department:    dept,
			EmployeeCount: directCount[dept.ID],
		}
	}

	var roots []*departmentTreeNode
	for i := range departments {
		node := nodes[departments[i].ID]
		parent, hasParent := nodes[node.ParentID]
		if hasParent && parent.ID != node.ID {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}

	// 数据异常（例如父子成环）时树会没有根节点，退化为平铺，避免前端拿到空列表
	if len(roots) == 0 && len(departments) > 0 {
		for i := range departments {
			roots = append(roots, nodes[departments[i].ID])
		}
	}

	// 自底向上汇总「含子部门」人数
	var accumulate func(node *departmentTreeNode) int64
	accumulate = func(node *departmentTreeNode) int64 {
		total := node.EmployeeCount
		for _, child := range node.Children {
			total += accumulate(child)
		}
		node.TotalEmployeeCount = total
		return total
	}
	for _, root := range roots {
		accumulate(root)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  roots,
		"total": len(departments),
	})
}

// 创建部门
func CreateDepartment(c *gin.Context) {
	var department models.Department

	if err := c.ShouldBindJSON(&department); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "请求参数错误",
			"message": err.Error(),
		})
		return
	}

	department.Name = strings.TrimSpace(department.Name)
	if department.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "部门名称不能为空",
		})
		return
	}

	// 校验上级部门
	if department.ParentID != 0 {
		var parent models.Department
		if err := models.DB.Select("id").First(&parent, department.ParentID).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "上级部门不存在",
			})
			return
		}
	}

	// 同一上级下不允许重名（企业微信也是这种约束）
	var duplicateCount int64
	models.DB.Model(&models.Department{}).
		Where("parent_id = ? AND name = ?", department.ParentID, department.Name).
		Count(&duplicateCount)
	if duplicateCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "同一上级部门下已存在同名部门",
		})
		return
	}

	result := models.DB.Create(&department)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "创建部门失败",
			"message": result.Error.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "部门创建成功",
		"data":    department,
	})
}

// 获取单个部门
func GetDepartment(c *gin.Context) {
	id := c.Param("id")
	departmentId, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的部门ID",
		})
		return
	}

	var department models.Department
	result := models.DB.Preload("Employees", "is_active = ?", true).First(&department, departmentId)
	if result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "部门不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": department,
	})
}

// 更新部门的请求体
// 使用指针区分「未传该字段」与「显式传 0/空字符串」，
// 否则 parent_id 无法被改回顶级部门（0 会被当成零值忽略）。
type departmentUpdateRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	ParentID    *uint   `json:"parent_id"`
}

// 更新部门
func UpdateDepartment(c *gin.Context) {
	id := c.Param("id")
	departmentId, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的部门ID",
		})
		return
	}

	var department models.Department
	result := models.DB.First(&department, departmentId)
	if result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "部门不存在",
		})
		return
	}

	var req departmentUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "请求参数错误",
			"message": err.Error(),
		})
		return
	}

	updates := map[string]interface{}{}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "部门名称不能为空",
			})
			return
		}
		updates["name"] = name
	}

	if req.Description != nil {
		updates["description"] = *req.Description
	}

	if req.ParentID != nil {
		newParentID := *req.ParentID

		// 不能把部门挂到自己下面
		if newParentID == department.ID {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "不能把部门设置为自己的上级",
			})
			return
		}

		// 不能挂到自己的子孙部门下（否则组织树会成环）
		if newParentID != 0 {
			var parent models.Department
			if err := models.DB.Select("id").First(&parent, newParentID).Error; err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "上级部门不存在",
				})
				return
			}

			subtreeIDs, err := models.CollectDepartmentSubtreeIDs(department.ID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "校验部门层级失败",
				})
				return
			}
			for _, subID := range subtreeIDs {
				if subID == newParentID {
					c.JSON(http.StatusBadRequest, gin.H{
						"error": "不能把部门移动到自己的子部门下",
					})
					return
				}
			}
		}

		updates["parent_id"] = newParentID
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "没有需要更新的字段",
		})
		return
	}

	result = models.DB.Model(&department).Updates(updates)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "更新部门失败",
			"message": result.Error.Error(),
		})
		return
	}

	models.DB.First(&department, departmentId)

	c.JSON(http.StatusOK, gin.H{
		"message": "部门更新成功",
		"data":    department,
	})
}

// 删除部门
func DeleteDepartment(c *gin.Context) {
	id := c.Param("id")
	departmentId, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的部门ID",
		})
		return
	}

	// 检查是否有子部门
	var childCount int64
	models.DB.Model(&models.Department{}).Where("parent_id = ?", departmentId).Count(&childCount)
	if childCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "该部门下还有子部门，请先处理子部门",
		})
		return
	}

	// 检查是否有员工仍挂在该部门
	// 注意要包含已停用/离职员工：他们同样占着 department_id，
	// 只查在职会允许删除，留下指向不存在部门的数据（列表里部门名变空）。
	var employeeCount int64
	models.DB.Model(&models.Employee{}).Where("department_id = ?", departmentId).Count(&employeeCount)
	if employeeCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "该部门下还有员工（含已停用），请先调整这些员工的所属部门",
		})
		return
	}

	result := models.DB.Delete(&models.Department{}, departmentId)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "删除部门失败",
			"message": result.Error.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "部门删除成功",
	})
}
