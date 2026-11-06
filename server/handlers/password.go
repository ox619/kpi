package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"dootask-kpi-server/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// 与注册相同，使用 bcrypt 默认成本保存密码，不保存或返回明文。
func validateAccountPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 6 && len(password) <= 72
}

func ChangePassword(c *gin.Context) {
	employeeID := c.GetUint("user_id")
	if employeeID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户未登录"})
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password" binding:"required"`
		NewPassword     string `json:"new_password" binding:"required"`
	}
	if c.ShouldBindJSON(&req) != nil || !validateAccountPassword(req.NewPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入当前密码，新密码至少 6 个字符且不超过 72 字节"})
		return
	}
	var user models.Employee
	if err := models.DB.First(&user, employeeID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取账号失败，请稍后重试"})
		return
	}
	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "账号已被禁用"})
		return
	}
	if user.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该账号尚未设置本地密码，请联系 HR 重置密码"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.CurrentPassword)) != nil {
		// 不使用 401，避免输错密码触发全局登出。
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前密码不正确"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码处理失败，请重试"})
		return
	}
	res := models.DB.Model(&models.Employee{}).
		Where("id = ? AND is_active = ? AND password = ?", employeeID, true, user.Password).
		Update("password", string(hash))
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "修改密码失败，请重试"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "账号或密码已变化，请刷新后重试"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "密码修改成功"})
}

func ResetEmployeePassword(c *gin.Context) {
	actorID := c.GetUint("user_id")
	if actorID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户未登录"})
		return
	}
	// 密码重置按数据库当前角色授权，不能凭降权前签发的旧 JWT 重置他人密码。
	var actor models.Employee
	if err := models.DB.First(&actor, actorID).Error; err != nil || !actor.IsActive || actor.Role != "hr" {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有在职 HR 可以重置员工密码"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的员工 ID"})
		return
	}
	var req struct {
		NewPassword string `json:"new_password" binding:"required"`
	}
	if c.ShouldBindJSON(&req) != nil || !validateAccountPassword(req.NewPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码至少 6 个字符且不超过 72 字节"})
		return
	}
	var employee models.Employee
	if err := models.DB.First(&employee, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "员工不存在"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取员工失败，请重试"})
		}
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码处理失败，请重试"})
		return
	}
	res := models.DB.Model(&models.Employee{}).Where("id = ? AND password = ?", employee.ID, employee.Password).
		Update("password", string(hash))
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重置密码失败，请重试"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "账号或密码已变化，请刷新后重试"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "密码已重置，请将新密码告知该员工"})
}
