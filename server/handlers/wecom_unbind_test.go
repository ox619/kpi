package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"dootask-kpi-server/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func setupWeComUnbindDB(t *testing.T) (*models.Employee, *models.Employee) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wecom.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Department{}, &models.Employee{}); err != nil {
		t.Fatal(err)
	}
	previous := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = previous
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	id1, id2 := "wecom-one", "wecom-two"
	first := &models.Employee{Name: "one", Email: "one@example.com", Password: string(hash), WeComUserID: &id1, IsActive: true}
	second := &models.Employee{Name: "two", Email: "two@example.com", Password: string(hash), WeComUserID: &id2, IsActive: true}
	if err := db.Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	return first, second
}

func callWeComUnbind(t *testing.T, id uint, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"password": password, "employee_id": id + 1})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/auth/wecom/binding", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", id)
	WeComUnbindHandler(c)
	return recorder
}

func TestWeComUnbindRequiresCurrentPassword(t *testing.T) {
	user, _ := setupWeComUnbindDB(t)
	for _, tc := range []struct {
		id       uint
		password string
		status   int
	}{
		{0, "correct-password", http.StatusUnauthorized},
		{user.ID, "", http.StatusBadRequest},
		{user.ID, "wrong-password", http.StatusBadRequest},
	} {
		if r := callWeComUnbind(t, tc.id, tc.password); r.Code != tc.status {
			t.Fatalf("expected %d, got %d: %s", tc.status, r.Code, r.Body.String())
		}
	}
	var current models.Employee
	models.DB.First(&current, user.ID)
	if current.WeComUserID == nil || current.WeComAuthVersion != 0 {
		t.Fatal("failed authentication changed binding")
	}
	models.DB.Model(user).Update("is_active", false)
	if r := callWeComUnbind(t, user.ID, "correct-password"); r.Code != http.StatusForbidden {
		t.Fatalf("disabled account accepted: %d", r.Code)
	}
}

func TestWeComUnbindRevokesOnlyCurrentAccountAndAllowsRebinding(t *testing.T) {
	user, other := setupWeComUnbindDB(t)
	t.Cleanup(func() { revokeWeComPendingAuth(user.ID); revokeWeComPendingAuth(other.ID) })
	state, nonce, err := issueWeComBindState(user.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := issueWeComLoginTicket(user.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	otherTicket, err := issueWeComLoginTicket(other.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := generateToken(user)
	if err != nil {
		t.Fatal(err)
	}
	r := callWeComUnbind(t, user.ID, "correct-password")
	if r.Code != http.StatusOK {
		t.Fatalf("unbind failed: %s", r.Body.String())
	}
	var current, unchanged models.Employee
	models.DB.First(&current, user.ID)
	models.DB.First(&unchanged, other.ID)
	if current.WeComUserID != nil || current.WeComAuthVersion != 1 {
		t.Fatalf("binding not cleared or version not advanced: %+v", current)
	}
	if unchanged.WeComUserID == nil || *unchanged.WeComUserID != *other.WeComUserID {
		t.Fatal("another account was changed")
	}
	if _, ok := consumeWeComBindState(state, nonce); ok {
		t.Fatal("old binding request survived")
	}
	if _, ok := consumeWeComLoginTicket(ticket); ok {
		t.Fatal("old ticket survived")
	}
	if _, ok := consumeWeComLoginTicket(otherTicket); !ok {
		t.Fatal("another account's ticket revoked")
	}
	if _, err := verifyToken(jwt); err != nil {
		t.Fatal("existing session invalidated")
	}
	if _, err := bindWeComUser(user.ID, "old-in-flight", 0); !errors.Is(err, errWeComBindConflict) {
		t.Fatalf("stale callback accepted: %v", err)
	}
	if _, err := bindWeComUser(user.ID, "new-identity", current.WeComAuthVersion); err != nil {
		t.Fatalf("new binding failed: %v", err)
	}
	for i := 0; i < 2; i++ {
		if r := callWeComUnbind(t, user.ID, "correct-password"); r.Code != http.StatusOK {
			t.Fatalf("repeated unbind failed: %s", r.Body.String())
		}
	}
}

func TestWeComUnbindRejectsLateOldTicketAfterRebinding(t *testing.T) {
	user, _ := setupWeComUnbindDB(t)
	t.Cleanup(func() { revokeWeComPendingAuth(user.ID) })
	if r := callWeComUnbind(t, user.ID, "correct-password"); r.Code != http.StatusOK {
		t.Fatal(r.Body.String())
	}
	if _, err := bindWeComUser(user.ID, "new-identity", 1); err != nil {
		t.Fatal(err)
	}
	// An old OAuth callback may issue its ticket after revocation has already run.
	stale, err := issueWeComLoginTicket(user.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := issueWeComLoginTicket(user.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ticket string
		status int
	}{{stale, http.StatusBadRequest}, {fresh, http.StatusOK}} {
		body, _ := json.Marshal(map[string]string{"ticket": tc.ticket})
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/wecom/exchange", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		WeComExchangeHandler(c)
		if r.Code != tc.status {
			t.Fatalf("expected %d, got %d: %s", tc.status, r.Code, r.Body.String())
		}
	}
}
