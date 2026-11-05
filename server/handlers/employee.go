package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dootask-kpi-server/models"

	"github.com/gin-gonic/gin"
)

// 获取所有员工
func GetEmployees(c *gin.Context) {
	var employees []models.Employee

	// 解析分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	search := c.Query("search")
	departmentID := c.Query("department_id")
	role := c.Query("role")
	isActiveStr := c.Query("is_active")

	// 验证分页参数
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	// 构建查询
	query := models.DB.Preload("Department").Preload("Manager")

	// 添加搜索条件
	if search != "" {
		searchPattern := "%" + search + "%"
		query = query.Where("name LIKE ? OR email LIKE ? OR position LIKE ?",
			searchPattern, searchPattern, searchPattern)
	}

	// 添加部门筛选（包含子部门）
	if departmentID != "" {
		query = applyDepartmentSubtreeScope(query, "department_id", departmentID)
	}

	// 添加角色筛选（支持多个角色，用逗号分隔）
	if role != "" {
		if role == "manager,hr" {
			query = query.Where("role IN (?)", []string{"manager", "hr"})
		} else {
			query = query.Where("role = ?", role)
		}
	}

	// 添加状态筛选（is_active）
	if isActiveStr != "" {
		isActive := isActiveStr == "true"
		query = query.Where("is_active = ?", isActive)
	}

	// 获取总数
	var total int64
	countQuery := models.DB.Model(&models.Employee{})
	if search != "" {
		searchPattern := "%" + search + "%"
		countQuery = countQuery.Where("name LIKE ? OR email LIKE ? OR position LIKE ?",
			searchPattern, searchPattern, searchPattern)
	}
	if departmentID != "" {
		countQuery = applyDepartmentSubtreeScope(countQuery, "department_id", departmentID)
	}
	if role != "" {
		if role == "manager,hr" {
			countQuery = countQuery.Where("role IN (?)", []string{"manager", "hr"})
		} else {
			countQuery = countQuery.Where("role = ?", role)
		}
	}
	if isActiveStr != "" {
		isActive := isActiveStr == "true"
		countQuery = countQuery.Where("is_active = ?", isActive)
	}

	if err := countQuery.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "获取员工总数失败",
			"message": err.Error(),
		})
		return
	}

	// 分页查询
	offset := (page - 1) * pageSize
	result := query.Offset(offset).Limit(pageSize).Find(&employees)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "获取员工列表失败",
			"message": result.Error.Error(),
		})
		return
	}

	// 计算分页信息
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	c.JSON(http.StatusOK, gin.H{
		"data":       employees,
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"totalPages": totalPages,
		"hasNext":    page < totalPages,
		"hasPrev":    page > 1,
	})
}

// 创建员工
func CreateEmployee(c *gin.Context) {
	var employee models.Employee

	if err := c.ShouldBindJSON(&employee); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "请求参数错误",
			"message": err.Error(),
		})
		return
	}

	if employee.Role == "" {
		employee.Role = "employee"
	}
	// 允许暂不配置上级；统一用 NULL，考核创建时再校验。
	if employee.ManagerID != nil && *employee.ManagerID == 0 {
		employee.ManagerID = nil
	}

	result := models.DB.Create(&employee)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "创建员工失败",
			"message": result.Error.Error(),
		})
		return
	}

	// 获取完整的员工信息
	models.DB.Preload("Department").Preload("Manager").First(&employee, employee.ID)

	c.JSON(http.StatusCreated, gin.H{
		"message": "员工创建成功",
		"data":    employee,
	})
}

// 获取单个员工
func GetEmployee(c *gin.Context) {
	id := c.Param("id")
	employeeId, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的员工ID",
		})
		return
	}

	var employee models.Employee
	result := models.DB.Preload("Department").Preload("Manager").Preload("Subordinates").First(&employee, employeeId)
	if result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "员工不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": employee,
	})
}

// 更新员工
func UpdateEmployee(c *gin.Context) {
	id := c.Param("id")
	employeeId, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的员工ID",
		})
		return
	}

	var employee models.Employee
	result := models.DB.First(&employee, employeeId)
	if result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "员工不存在",
		})
		return
	}

	var updateData struct {
		models.Employee
		ManagerID json.RawMessage `json:"manager_id"`
	}
	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "请求参数错误",
			"message": err.Error(),
		})
		return
	}

	roleValue := updateData.Role
	if roleValue == "" {
		roleValue = employee.Role
	}
	// 区分未传字段（保留原上级）与显式 null（清空），不依赖是否同时提交角色。
	managerValue := employee.ManagerID
	if len(updateData.ManagerID) > 0 {
		if err := json.Unmarshal(updateData.ManagerID, &managerValue); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "直属上级 ID 无效"})
			return
		}
	}
	if managerValue != nil && *managerValue == 0 {
		managerValue = nil
	}

	// 使用 map 来确保 nil 值能够被正确更新
	updateMap := map[string]interface{}{
		"name":          updateData.Name,
		"email":         models.NormalizeEmail(updateData.Email),
		"position":      updateData.Position,
		"department_id": updateData.DepartmentID,
		"role":          roleValue,
		"is_active":     updateData.IsActive,
	}

	managerProvided := len(updateData.ManagerID) > 0
	if managerProvided {
		updateMap["manager_id"] = managerValue
	}

	query := models.DB.Model(&employee)
	clearingManager := managerProvided && managerValue == nil
	if clearingManager {
		// 按写入时的数据库状态判断：原本无上级可保存，有上级则仅在无进行中考核时允许清空。
		// 与资料更新放在同一条 SQL，避免并发新增考核或分配上级导致误清空。
		query = query.Where(
			"(COALESCE(manager_id, 0) = 0 OR NOT EXISTS (SELECT 1 FROM kpi_evaluations WHERE employee_id = ? AND COALESCE(status, '') <> ?))",
			employee.ID, "completed",
		)
	}
	result = query.Updates(updateMap)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "更新员工失败",
			"message": result.Error.Error(),
		})
		return
	}

	if result.RowsAffected == 0 && clearingManager {
		c.JSON(http.StatusConflict, gin.H{
			"error": "该员工有进行中的考核，不能清空直属上级；请保留上级、指定新上级或完成考核后再操作",
		})
		return
	}

	// 获取完整的员工信息
	models.DB.Preload("Department").Preload("Manager").First(&employee, employee.ID)

	c.JSON(http.StatusOK, gin.H{
		"message": "员工更新成功",
		"data":    employee,
	})
}

// 删除员工
func DeleteEmployee(c *gin.Context) {
	id := c.Param("id")
	employeeId, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的员工ID",
		})
		return
	}

	// 检查是否有下属员工
	var subordinateCount int64
	models.DB.Model(&models.Employee{}).Where("manager_id = ?", employeeId).Count(&subordinateCount)
	if subordinateCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "该员工还有下属，无法删除",
		})
		return
	}

	// 检查是否有相关的KPI评估记录
	var evaluationCount int64
	models.DB.Model(&models.KPIEvaluation{}).Where("employee_id = ?", employeeId).Count(&evaluationCount)
	if evaluationCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "该员工有KPI评估记录，无法删除",
		})
		return
	}

	result := models.DB.Delete(&models.Employee{}, employeeId)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "删除员工失败",
			"message": result.Error.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "员工删除成功",
	})
}

// 获取员工的下属列表
func GetEmployeeSubordinates(c *gin.Context) {
	id := c.Param("id")
	employeeId, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的员工ID",
		})
		return
	}

	var subordinates []models.Employee
	result := models.DB.Preload("Department").Where("manager_id = ?", employeeId).Find(&subordinates)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "获取下属列表失败",
			"message": result.Error.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  subordinates,
		"total": len(subordinates),
	})
}
