package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUserKey(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := UserKey(c); got != "u0" {
		t.Errorf("anonymous = %q, want u0", got)
	}
	c.Set(ctxClaimsKey, &Claims{UserID: 42})
	if got := UserKey(c); got != "u42" {
		t.Errorf("user 42 = %q, want u42", got)
	}
}
