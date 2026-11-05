package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dootask-kpi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func setupOptionalManagerDB(t *testing.T) *models.Department {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "registration.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Department{}, &models.Employee{}, &models.SystemSetting{}, &models.KPIEvaluation{}, &models.KPITemplate{}); err != nil {
		t.Fatal(err)
	}
	previous := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = previous
		sqlDB, err := db.DB()
		if err == nil {
			sqlDB.Close()
		}
	})
	department := &models.Department{Name: "No manager yet"}
	if err := db.Create(department).Error; err != nil {
		t.Fatal(err)
	}
	return department
}

func callOptionalManagerHandler(t *testing.T, handler gin.HandlerFunc, method string, body map[string]interface{}, id uint) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, "/", bytes.NewReader(data))
	c.Request.Header.Set("Content-Type", "application/json")
	if id != 0 {
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(id), 10)}}
	}
	handler(c)
	return recorder
}

func registrationBody(departmentID uint) map[string]interface{} {
	return map[string]interface{}{
		"name": "New employee", "email": "new@example.com",
		"password": "test-password", "department_id": departmentID,
	}
}

func TestRegisterWithOptionalDepartmentManager(t *testing.T) {
	for _, kind := range []string{"none", "employee-only", "disabled-manager", "disabled-hr", "other-department", "manager", "hr"} {
		t.Run(kind, func(t *testing.T) {
			department := setupOptionalManagerDB(t)
			var supervisor models.Employee
			if kind != "none" {
				role := "manager"
				if kind == "hr" || kind == "disabled-hr" {
					role = "hr"
				} else if kind == "employee-only" {
					role = "employee"
				}
				supervisor = models.Employee{Name: "Supervisor", Email: "supervisor@example.com", Password: "unused", DepartmentID: department.ID, Role: role, IsActive: true}
				if kind == "other-department" {
					other := models.Department{Name: "Other"}
					if err := models.DB.Create(&other).Error; err != nil {
						t.Fatal(err)
					}
					supervisor.DepartmentID = other.ID
				}
				if err := models.DB.Create(&supervisor).Error; err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(kind, "disabled-") {
					if err := models.DB.Model(&supervisor).Update("is_active", false).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			response := callOptionalManagerHandler(t, Register, http.MethodPost, registrationBody(department.ID), 0)
			if response.Code != http.StatusCreated {
				t.Fatalf("registration failed: %d %s", response.Code, response.Body.String())
			}
			var session LoginResponse
			if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			if session.User == nil || session.User.Role != "employee" || !session.User.IsActive {
				t.Fatal("registration must create an active ordinary employee")
			}
			if claims, err := verifyToken(session.Token); err != nil || claims.UserID != session.User.ID {
				t.Fatalf("registration session invalid: %v", err)
			}
			var saved models.Employee
			if err := models.DB.First(&saved, session.User.ID).Error; err != nil {
				t.Fatal(err)
			}
			if bcrypt.CompareHashAndPassword([]byte(saved.Password), []byte("test-password")) != nil {
				t.Fatal("registered password is invalid")
			}
			if kind == "manager" || kind == "hr" {
				if saved.ManagerID == nil || *saved.ManagerID != supervisor.ID {
					t.Fatal("active department supervisor not assigned")
				}
			} else if saved.ManagerID != nil {
				t.Fatal("unavailable supervisor must be stored as NULL")
			}
		})
	}
}

func TestRegisterKeepsDepartmentAndRegistrationChecks(t *testing.T) {
	t.Run("missing-department", func(t *testing.T) {
		department := setupOptionalManagerDB(t)
		r := callOptionalManagerHandler(t, Register, http.MethodPost, registrationBody(department.ID+100), 0)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("missing department accepted: %s", r.Body.String())
		}
	})
	t.Run("registration-disabled", func(t *testing.T) {
		department := setupOptionalManagerDB(t)
		if err := models.DB.Create(&models.SystemSetting{Key: "allow_registration", Value: "false"}).Error; err != nil {
			t.Fatal(err)
		}
		r := callOptionalManagerHandler(t, Register, http.MethodPost, registrationBody(department.ID), 0)
		if r.Code != http.StatusForbidden {
			t.Fatalf("closed registration accepted: %s", r.Body.String())
		}
	})
	t.Run("manager-query-error", func(t *testing.T) {
		department := setupOptionalManagerDB(t)
		if err := models.DB.Callback().Query().After("gorm:query").Register("test:fail-manager-query", func(tx *gorm.DB) {
			if tx.Statement.Table == "employees" && strings.Contains(tx.Statement.SQL.String(), "role IN") {
				tx.AddError(errors.New("simulated manager query failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		r := callOptionalManagerHandler(t, Register, http.MethodPost, registrationBody(department.ID), 0)
		if r.Code != http.StatusInternalServerError {
			t.Fatalf("query error treated as no supervisor: %d %s", r.Code, r.Body.String())
		}
		var count int64
		models.DB.Model(&models.Employee{}).Count(&count)
		if count != 0 {
			t.Fatal("account created despite manager query error")
		}
	})
}

func TestEmployeeWithoutManagerCanSaveAndMustConfigureBeforeEvaluation(t *testing.T) {
	department := setupOptionalManagerDB(t)
	body := map[string]interface{}{
		"name": "Employee", "email": "employee@example.com", "department_id": department.ID,
		"role": "employee", "is_active": true, "manager_id": 0,
	}
	r := callOptionalManagerHandler(t, CreateEmployee, http.MethodPost, body, 0)
	if r.Code != http.StatusCreated {
		t.Fatalf("employee without manager cannot be created: %s", r.Body.String())
	}
	var saved models.Employee
	if err := models.DB.Where("email = ?", "employee@example.com").First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.ManagerID != nil {
		t.Fatal("zero manager ID not normalized to NULL")
	}
	body["name"] = "Updated employee"
	body["manager_id"] = nil
	r = callOptionalManagerHandler(t, UpdateEmployee, http.MethodPut, body, saved.ID)
	if r.Code != http.StatusOK {
		t.Fatalf("employee without manager cannot save profile: %s", r.Body.String())
	}
	models.DB.First(&saved, saved.ID)
	if saved.Name != "Updated employee" || saved.ManagerID != nil {
		t.Fatal("profile not saved with NULL manager")
	}
	r = callOptionalManagerHandler(t, CreateEvaluation, http.MethodPost, map[string]interface{}{"employee_id": saved.ID}, 0)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "暂无直属上级") {
		t.Fatalf("evaluation allowed without supervisor: %d %s", r.Code, r.Body.String())
	}
	supervisor := models.Employee{Name: "Manager", Email: "manager@example.com", Password: "unused", DepartmentID: department.ID, Role: "manager", IsActive: true}
	if err := models.DB.Create(&supervisor).Error; err != nil {
		t.Fatal(err)
	}
	body["manager_id"] = supervisor.ID
	r = callOptionalManagerHandler(t, UpdateEmployee, http.MethodPut, body, saved.ID)
	if r.Code != http.StatusOK {
		t.Fatalf("cannot assign supervisor later: %s", r.Body.String())
	}
	models.DB.First(&saved, saved.ID)
	if saved.ManagerID == nil || *saved.ManagerID != supervisor.ID {
		t.Fatal("supervisor assignment not saved")
	}
}

func TestRegisterManagerSelectionPriority(t *testing.T) {
	for _, roles := range [][]string{{"hr", "manager", "manager", "hr"}, {"manager", "hr"}, {"hr", "hr"}, {"manager", "manager"}} {
		t.Run(strings.Join(roles, "-"), func(t *testing.T) {
			department := setupOptionalManagerDB(t)
			var expected uint
			managerFound := false
			for i, role := range roles {
				person := models.Employee{Name: role, Email: strconv.Itoa(i) + "@example.com", Password: "unused", DepartmentID: department.ID, Role: role, IsActive: true}
				if err := models.DB.Create(&person).Error; err != nil {
					t.Fatal(err)
				}
				if expected == 0 || (role == "manager" && !managerFound) {
					expected = person.ID
				}
				managerFound = managerFound || role == "manager"
			}
			for i := 0; i < 2; i++ {
				body := registrationBody(department.ID)
				body["email"] = "new-" + strconv.Itoa(i) + "@example.com"
				r := callOptionalManagerHandler(t, Register, http.MethodPost, body, 0)
				if r.Code != http.StatusCreated {
					t.Fatal(r.Body.String())
				}
				var session LoginResponse
				if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
					t.Fatal(err)
				}
				if session.User.ManagerID == nil || *session.User.ManagerID != expected {
					t.Fatalf("expected supervisor %d, got %v", expected, session.User.ManagerID)
				}
			}
		})
	}
}

func TestCannotClearManagerWithOngoingEvaluations(t *testing.T) {
	for _, status := range []string{"pending", "self_evaluated", "manager_evaluated", "pending_confirm", "completed", "none"} {
		for _, mode := range []string{"explicit-null", "zero", "omitted"} {
			t.Run(status+"/"+mode, func(t *testing.T) {
				department := setupOptionalManagerDB(t)
				manager := models.Employee{Name: "Manager", Email: "manager@example.com", Password: "unused", DepartmentID: department.ID, Role: "manager", IsActive: true}
				if err := models.DB.Create(&manager).Error; err != nil {
					t.Fatal(err)
				}
				employee := models.Employee{Name: "Original name", Email: "employee@example.com", Password: "unused", DepartmentID: department.ID, Role: "employee", ManagerID: &manager.ID, IsActive: true}
				if err := models.DB.Create(&employee).Error; err != nil {
					t.Fatal(err)
				}
				if status != "none" {
					if err := models.DB.Create(&models.KPIEvaluation{EmployeeID: employee.ID, Status: status}).Error; err != nil {
						t.Fatal(err)
					}
				}
				// Omit role too: manager-field presence must determine preservation/clearing.
				body := map[string]interface{}{
					"name": "Changed name", "email": employee.Email,
					"department_id": department.ID, "is_active": true,
				}
				switch mode {
				case "explicit-null":
					body["manager_id"] = nil
				case "zero":
					body["manager_id"] = 0
				}
				r := callOptionalManagerHandler(t, UpdateEmployee, http.MethodPut, body, employee.ID)
				ongoing := status != "none" && status != "completed"
				blocked := ongoing && mode != "omitted"
				expectedStatus := http.StatusOK
				if blocked {
					expectedStatus = http.StatusConflict
				}
				if r.Code != expectedStatus {
					t.Fatalf("expected %d, got %d: %s", expectedStatus, r.Code, r.Body.String())
				}
				var saved models.Employee
				if err := models.DB.First(&saved, employee.ID).Error; err != nil {
					t.Fatal(err)
				}
				if blocked && (saved.ManagerID == nil || *saved.ManagerID != manager.ID || saved.Name != employee.Name) {
					t.Fatal("rejected update changed supervisor or profile")
				}
				if mode == "omitted" && (saved.ManagerID == nil || *saved.ManagerID != manager.ID) {
					t.Fatal("omitting manager_id cleared supervisor")
				}
				if !ongoing && mode != "omitted" && saved.ManagerID != nil {
					t.Fatal("cannot clear supervisor after evaluations complete")
				}
				if ongoing {
					replacement := models.Employee{Name: "Replacement", Email: "replacement@example.com", Password: "unused", DepartmentID: department.ID, Role: "manager", IsActive: true}
					if err := models.DB.Create(&replacement).Error; err != nil {
						t.Fatal(err)
					}
					body["manager_id"] = replacement.ID
					r = callOptionalManagerHandler(t, UpdateEmployee, http.MethodPut, body, employee.ID)
					if r.Code != http.StatusOK {
						t.Fatalf("valid replacement rejected: %s", r.Body.String())
					}
				}
			})
		}
	}
}

func TestConcurrentClearManagerCannotCreateStrandedEvaluation(t *testing.T) {
	department := setupOptionalManagerDB(t)
	if err := models.DB.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		t.Fatal(err)
	}
	manager := models.Employee{Name: "Manager", Email: "manager@example.com", Password: "unused", DepartmentID: department.ID, Role: "manager", IsActive: true}
	if err := models.DB.Create(&manager).Error; err != nil {
		t.Fatal(err)
	}
	employee := models.Employee{Name: "Employee", Email: "employee@example.com", Password: "unused", DepartmentID: department.ID, Role: "employee", ManagerID: &manager.ID, IsActive: true}
	if err := models.DB.Create(&employee).Error; err != nil {
		t.Fatal(err)
	}
	interleaved := false
	if err := models.DB.Callback().Query().After("gorm:query").Register("test:clear-after-manager-read", func(tx *gorm.DB) {
		if interleaved || tx.Statement.Table != "employees" {
			return
		}
		loaded, ok := tx.Statement.Dest.(*models.Employee)
		if !ok || loaded.ID != employee.ID {
			return
		}
		interleaved = true
		// A second connection clears the supervisor after the evaluation read.
		body := map[string]interface{}{
			"name": employee.Name, "email": employee.Email, "department_id": department.ID,
			"is_active": true, "role": "employee", "manager_id": nil,
		}
		r := callOptionalManagerHandler(t, UpdateEmployee, http.MethodPut, body, employee.ID)
		if r.Code != http.StatusOK {
			t.Fatalf("concurrent clear failed: %s", r.Body.String())
		}
	}); err != nil {
		t.Fatal(err)
	}
	r := callOptionalManagerHandler(t, CreateEvaluation, http.MethodPost, map[string]interface{}{"employee_id": employee.ID}, 0)
	if !interleaved {
		t.Fatal("test did not interleave supervisor removal")
	}
	if r.Code == http.StatusCreated {
		t.Fatal("evaluation created using a stale supervisor read")
	}
	var count int64
	if err := models.DB.Model(&models.KPIEvaluation{}).Where("employee_id = ?", employee.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stranded evaluation persisted")
	}
}

func TestEmployeeAlreadyWithoutManagerCanEditDuringEvaluation(t *testing.T) {
	for _, status := range []string{"pending", "self_evaluated", "manager_evaluated", "pending_confirm"} {
		for _, mode := range []string{"explicit-null", "zero", "omitted"} {
			t.Run(status+"/"+mode, func(t *testing.T) {
				department := setupOptionalManagerDB(t)
				employee := models.Employee{
					Name: "Original name", Email: "unassigned@example.com", Password: "unused",
					DepartmentID: department.ID, Role: "employee", IsActive: true,
				}
				if err := models.DB.Create(&employee).Error; err != nil {
					t.Fatal(err)
				}
				// Legacy/imported records can already have an active evaluation without a manager.
				if err := models.DB.Create(&models.KPIEvaluation{EmployeeID: employee.ID, Status: status}).Error; err != nil {
					t.Fatal(err)
				}
				body := map[string]interface{}{
					"name": "Updated name", "email": employee.Email, "position": "Updated position",
					"department_id": department.ID, "role": "employee", "is_active": true,
				}
				switch mode {
				case "explicit-null":
					body["manager_id"] = nil
				case "zero":
					body["manager_id"] = 0
				}
				r := callOptionalManagerHandler(t, UpdateEmployee, http.MethodPut, body, employee.ID)
				if r.Code != http.StatusOK {
					t.Fatalf("profile edit incorrectly blocked: %d %s", r.Code, r.Body.String())
				}
				var saved models.Employee
				if err := models.DB.First(&saved, employee.ID).Error; err != nil {
					t.Fatal(err)
				}
				if saved.Name != "Updated name" || saved.Position != "Updated position" || saved.ManagerID != nil {
					t.Fatal("profile edit did not preserve missing manager")
				}
			})
		}
	}
}
func TestOmittingManagerDoesNotOverwriteConcurrentAssignment(t *testing.T) {
	department := setupOptionalManagerDB(t)
	manager := models.Employee{Name: "Manager", Email: "manager@example.com", Password: "unused", DepartmentID: department.ID, Role: "manager", IsActive: true}
	if err := models.DB.Create(&manager).Error; err != nil {
		t.Fatal(err)
	}
	employee := models.Employee{Name: "Original name", Email: "employee@example.com", Password: "unused", DepartmentID: department.ID, Role: "employee", IsActive: true}
	if err := models.DB.Create(&employee).Error; err != nil {
		t.Fatal(err)
	}
	assigned := false
	if err := models.DB.Callback().Query().After("gorm:query").Register("test:assign-manager-after-read", func(tx *gorm.DB) {
		if assigned || tx.Statement.Table != "employees" {
			return
		}
		loaded, ok := tx.Statement.Dest.(*models.Employee)
		if !ok || loaded.ID != employee.ID {
			return
		}
		assigned = true
		if err := models.DB.Exec("UPDATE employees SET manager_id = ? WHERE id = ?", manager.ID, employee.ID).Error; err != nil {
			t.Fatal(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	body := map[string]interface{}{
		"name": "Updated name", "email": employee.Email, "department_id": department.ID,
		"role": "employee", "is_active": true,
	}
	r := callOptionalManagerHandler(t, UpdateEmployee, http.MethodPut, body, employee.ID)
	if r.Code != http.StatusOK {
		t.Fatalf("profile update failed: %s", r.Body.String())
	}
	var saved models.Employee
	if err := models.DB.First(&saved, employee.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !assigned || saved.ManagerID == nil || *saved.ManagerID != manager.ID || saved.Name != "Updated name" {
		t.Fatal("omitted manager field overwrote concurrent assignment")
	}
}
