package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestWeComBindState(t *testing.T) (string, string) {
	t.Helper()
	state, nonce, err := issueWeComBindState(42, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		wecomBindRequests.Lock()
		delete(wecomBindRequests.items, nonce)
		wecomBindRequests.Unlock()
	})
	return state, nonce
}

func TestWeComBindStateRejectsInvalidBrowserAndSignature(t *testing.T) {
	for _, kind := range []string{"missing-cookie", "wrong-cookie", "tampered-state", "invalid-state"} {
		t.Run(kind, func(t *testing.T) {
			state, nonce := newTestWeComBindState(t)
			badState, badCookie := state, nonce
			switch kind {
			case "missing-cookie":
				badCookie = ""
			case "wrong-cookie":
				badCookie = "another-browser"
			case "tampered-state":
				badState += "0"
			case "invalid-state":
				badState = "b.invalid"
			}
			if _, ok := consumeWeComBindState(badState, badCookie); ok {
				t.Fatal("invalid callback accepted")
			}
			// An unrelated browser must not consume the legitimate request.
			if id, ok := consumeWeComBindState(state, nonce); !ok || id.employeeID != 42 {
				t.Fatal("legitimate request was lost")
			}
			if _, ok := consumeWeComBindState(state, nonce); ok {
				t.Fatal("replayed state accepted")
			}
		})
	}
}

func TestWeComBindStateExpired(t *testing.T) {
	state, nonce := newTestWeComBindState(t)
	wecomBindRequests.Lock()
	item := wecomBindRequests.items[nonce]
	item.expiresAt = time.Now().Add(-time.Minute)
	wecomBindRequests.items[nonce] = item
	wecomBindRequests.Unlock()
	if _, ok := consumeWeComBindState(state, nonce); ok {
		t.Fatal("expired state accepted")
	}
}

func TestWeComBindStateConcurrentConsumption(t *testing.T) {
	state, nonce := newTestWeComBindState(t)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := consumeWeComBindState(state, nonce); ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("expected one successful consumer, got %d", successes.Load())
	}
}

func TestWeComBindCallbackClearsCookieAndConsumesBeforeCode(t *testing.T) {
	for _, present := range []bool{false, true} {
		state, nonce := newTestWeComBindState(t)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", "http://kpi.example/api/auth/wecom/callback", nil)
		if present {
			c.Request.AddCookie(&http.Cookie{Name: wecomBindStateCookie, Value: nonce})
		}
		// No code: a verified request is still consumed before any external API call.
		handleWeComBindCallback(c, nil, "http://kpi.example", state)
		if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "http://kpi.example/settings?wecom="+map[bool]string{false: "browser", true: "error"}[present] {
			t.Fatal("expected error redirect")
		}
		cookies := recorder.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != wecomBindStateCookie || cookies[0].MaxAge != -1 {
			t.Fatal("callback did not clear binding cookie")
		}
		_, ok := consumeWeComBindState(state, nonce)
		if ok == present {
			t.Fatal("callback consumed incorrect request")
		}
	}
}

func TestWeComBindCookieAttributes(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "http://kpi.example/api/auth/wecom/bind/start", nil)
		c.Request.Header.Set("X-Forwarded-Proto", scheme)
		setWeComBindCookie(c, "nonce", 600)
		cookie := recorder.Result().Cookies()[0]
		if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode ||
			cookie.Secure != (scheme == "https") || cookie.Path != "/" ||
			cookie.Domain != "" || cookie.MaxAge != 600 {
			t.Fatalf("unexpected cookie attributes: %+v", cookie)
		}
	}
}

func TestWeComLoginTicketSingleUse(t *testing.T) {
	ticket, err := issueWeComLoginTicket(42, 0)
	if err != nil {
		t.Fatalf("签发 ticket 失败: %v", err)
	}

	employeeID, ok := consumeWeComLoginTicket(ticket)
	if !ok || employeeID.employeeID != 42 {
		t.Fatalf("ticket 应首次校验通过，实际 ok=%v employeeID=%d", ok, employeeID.employeeID)
	}

	if _, ok := consumeWeComLoginTicket(ticket); ok {
		t.Fatal("ticket 只能使用一次，第二次应失败")
	}
}

func TestWeComLoginTicketRejectsInvalidInput(t *testing.T) {
	for _, ticket := range []string{"", "   ", "not-a-ticket"} {
		if _, ok := consumeWeComLoginTicket(ticket); ok {
			t.Fatalf("非法 ticket 应校验失败: %q", ticket)
		}
	}
}

func TestWeComLoginTicketExpired(t *testing.T) {
	const ticket = "expired-ticket-for-test"

	wecomLoginTickets.Lock()
	wecomLoginTickets.items[ticket] = wecomLoginTicket{
		employeeID: 42,
		expiresAt:  time.Now().Add(-time.Minute),
	}
	wecomLoginTickets.Unlock()

	if _, ok := consumeWeComLoginTicket(ticket); ok {
		t.Fatal("过期 ticket 应校验失败")
	}
}
