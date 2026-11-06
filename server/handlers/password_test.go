package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dootask-kpi-server/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func passwordTestAccount(t *testing.T, role, email, password string, departmentID uint) *models.Employee {
	t.Helper()
	hash := ""
	if password != "" {
		b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		hash = string(b)
	}
	user := &models.Employee{Name: role, Email: email, Password: hash, DepartmentID: departmentID, Role: role, IsActive: true}
	if err := models.DB.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func passwordTestRouter() *gin.Engine {
	r := gin.New()
	r.POST("/api/auth/login", Login)
	protected := r.Group("/api", AuthMiddleware())
	protected.POST("/employees", RoleMiddleware("hr", "manager"), CreateEmployee)
	protected.PUT("/auth/password", ChangePassword)
	protected.POST("/employees/:id/password", ResetEmployeePassword)
	return r
}

func passwordTestCall(t *testing.T, r http.Handler, method, path string, user *models.Employee, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if user != nil {
		token, err := generateToken(user)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func assertPasswordLogin(t *testing.T, r http.Handler, email, password string, status int) {
	t.Helper()
	w := passwordTestCall(t, r, http.MethodPost, "/api/auth/login", nil, map[string]interface{}{"email": email, "password": password})
	if w.Code != status {
		t.Fatalf("login: expected %d, got %d", status, w.Code)
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Fatal("login response exposes password field")
	}
}

func TestHRCreateEmployeeWithInitialPassword(t *testing.T) {
	department := setupOptionalManagerDB(t)
	hr := passwordTestAccount(t, "hr", "hr@example.com", "hr-password", department.ID)
	router := passwordTestRouter()
	body := map[string]interface{}{
		"name": "New employee", "email": "created@example.com", "password": "initial-password",
		"department_id": department.ID, "role": "employee", "is_active": true,
	}
	w := passwordTestCall(t, router, http.MethodPost, "/api/employees", hr, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("creation failed: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Fatal("creation response exposes password")
	}
	var user models.Employee
	if err := models.DB.Where("email = ?", "created@example.com").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.Password == "initial-password" || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte("initial-password")) != nil {
		t.Fatal("initial password is not stored as bcrypt")
	}
	assertPasswordLogin(t, router, user.Email, "initial-password", http.StatusOK)
	assertPasswordLogin(t, router, user.Email, "wrong-password", http.StatusUnauthorized)
	for _, password := range []string{"", "12345", strings.Repeat("a", 73), strings.Repeat("密", 25)} {
		body["email"] = "invalid@example.com"
		body["password"] = password
		w := passwordTestCall(t, router, http.MethodPost, "/api/employees", hr, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid initial password accepted: %d", w.Code)
		}
	}
}

func TestChangePasswordOnlyAffectsCurrentAccount(t *testing.T) {
	department := setupOptionalManagerDB(t)
	user := passwordTestAccount(t, "employee", "user@example.com", "old-password", department.ID)
	other := passwordTestAccount(t, "employee", "other@example.com", "other-password", department.ID)
	router := passwordTestRouter()
	body := map[string]interface{}{"current_password": "wrong-password", "new_password": "new-password", "employee_id": other.ID}
	w := passwordTestCall(t, router, http.MethodPut, "/api/auth/password", user, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong current password accepted: %d", w.Code)
	}
	body["current_password"] = "old-password"
	body["new_password"] = "short"
	w = passwordTestCall(t, router, http.MethodPut, "/api/auth/password", user, body)
	if w.Code != http.StatusBadRequest {
		t.Fatal("short new password accepted")
	}
	body["new_password"] = "new-password"
	w = passwordTestCall(t, router, http.MethodPut, "/api/auth/password", nil, body)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated password change accepted")
	}
	w = passwordTestCall(t, router, http.MethodPut, "/api/auth/password", user, body)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("password change failed or exposed password: %d", w.Code)
	}
	assertPasswordLogin(t, router, user.Email, "old-password", http.StatusUnauthorized)
	assertPasswordLogin(t, router, user.Email, "new-password", http.StatusOK)
	assertPasswordLogin(t, router, other.Email, "other-password", http.StatusOK)
}

func TestOnlyCurrentActiveHRCanResetPassword(t *testing.T) {
	department := setupOptionalManagerDB(t)
	hr := passwordTestAccount(t, "hr", "hr@example.com", "hr-password", department.ID)
	manager := passwordTestAccount(t, "manager", "manager@example.com", "manager-password", department.ID)
	employee := passwordTestAccount(t, "employee", "employee@example.com", "old-password", department.ID)
	legacy := passwordTestAccount(t, "employee", "legacy@example.com", "", department.ID)
	router := passwordTestRouter()
	path := "/api/employees/" + strconv.FormatUint(uint64(employee.ID), 10) + "/password"
	body := map[string]interface{}{"new_password": "reset-password"}
	for _, actor := range []*models.Employee{manager, employee} {
		w := passwordTestCall(t, router, http.MethodPost, path, actor, body)
		if w.Code != http.StatusForbidden {
			t.Fatalf("non-HR reset accepted: %d", w.Code)
		}
	}
	if w := passwordTestCall(t, router, http.MethodPost, path, nil, body); w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated reset accepted")
	}
	assertPasswordLogin(t, router, employee.Email, "old-password", http.StatusOK)
	w := passwordTestCall(t, router, http.MethodPost, path, hr, body)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("HR reset failed or exposed password: %d", w.Code)
	}
	assertPasswordLogin(t, router, employee.Email, "old-password", http.StatusUnauthorized)
	assertPasswordLogin(t, router, employee.Email, "reset-password", http.StatusOK)
	legacyPath := "/api/employees/" + strconv.FormatUint(uint64(legacy.ID), 10) + "/password"
	w = passwordTestCall(t, router, http.MethodPost, legacyPath, hr, body)
	if w.Code != http.StatusOK {
		t.Fatalf("cannot set password for legacy account: %d", w.Code)
	}
	assertPasswordLogin(t, router, legacy.Email, "reset-password", http.StatusOK)
	body["new_password"] = "short"
	if w := passwordTestCall(t, router, http.MethodPost, path, hr, body); w.Code != http.StatusBadRequest {
		t.Fatal("short reset password accepted")
	}
	body["new_password"] = "next-password"
	if w := passwordTestCall(t, router, http.MethodPost, "/api/employees/99999/password", hr, body); w.Code != http.StatusNotFound {
		t.Fatal("missing employee not rejected")
	}
	// Keep the HR claim in the test token, but demote the persisted account.
	if err := models.DB.Model(&models.Employee{}).Where("id = ?", hr.ID).Update("role", "employee").Error; err != nil {
		t.Fatal(err)
	}
	if w := passwordTestCall(t, router, http.MethodPost, path, hr, body); w.Code != http.StatusForbidden {
		t.Fatal("stale HR JWT allowed password reset after demotion")
	}
	if err := models.DB.Model(&models.Employee{}).Where("id = ?", hr.ID).Updates(map[string]interface{}{"role": "hr", "is_active": false}).Error; err != nil {
		t.Fatal(err)
	}
	if w := passwordTestCall(t, router, http.MethodPost, path, hr, body); w.Code != http.StatusUnauthorized {
		t.Fatal("inactive HR allowed password reset")
	}
}

func TestAccountWithoutPasswordMustUseHRReset(t *testing.T) {
	department := setupOptionalManagerDB(t)
	legacy := passwordTestAccount(t, "employee", "legacy@example.com", "", department.ID)
	w := passwordTestCall(t, passwordTestRouter(), http.MethodPut, "/api/auth/password", legacy,
		map[string]interface{}{"current_password": "anything", "new_password": "new-password"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "联系 HR") {
		t.Fatal("missing local password did not direct user to HR reset")
	}
}
