package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

const cancelHash = "2222222222222222222222222222222222222222"

func cancelledGinCtx(uid int) *gin.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	c.Set("auth.claims", &auth.Claims{UserID: uid, Username: "u", Role: auth.RoleUser})
	return c
}

// Curtain decisions must not depend on the request staying connected: with the
// request ctx already done, hidden favourites are still in the filter set and a
// hidden local path is still resolved (a cancelled read used to read as "nothing
// hidden").
func TestCurtainReads_CancelledRequest_StillFilter(t *testing.T) {
	_, s := hiddenIdentityEnv(t, cancelHash, cancelHash)
	const uid = 7
	if err := s.Favorites().SetLocalPathHidden(context.Background(), uid, "Movies", "secret", true); err != nil {
		t.Fatal(err)
	}

	c := cancelledGinCtx(uid)
	curtain := hiddenCurtain(c, s, uid, false)
	if !curtain.Hashes[cancelHash] || !curtain.Names[lowerFav()] {
		t.Errorf("hiddenCurtain with a cancelled ctx lost the hidden set: %+v", curtain)
	}
	f := buildHiddenDownloadFilter(cancelledGinCtx(uid), s, nil, nil, uid, false)
	if !f.curtain.Hashes[cancelHash] {
		t.Errorf("buildHiddenDownloadFilter with a cancelled ctx lost the hidden set: %+v", f.curtain)
	}
	owned := collectHiddenLocalPaths(cancelledGinCtx(uid).Request.Context(), s, uid, false)
	if len(owned) != 1 || owned[0].Path != "secret" {
		t.Errorf("collectHiddenLocalPaths with a cancelled ctx = %+v; want the hidden path", owned)
	}
	if got := recHiddenCurtain(cancelledGinCtx(uid).Request.Context(), s, uid); !got.Hashes[cancelHash] {
		t.Errorf("recHiddenCurtain with a cancelled ctx lost the hidden set: %+v", got)
	}
	e := buildEnricher(cancelledGinCtx(uid).Request.Context(), s.Favorites(), nil, uid, false, false)
	if e.favHashes[cancelHash] {
		t.Errorf("buildEnricher with a cancelled ctx leaked the hidden favourite heart")
	}
}

func lowerFav() string { return "some.show.s01e01.1080p.web-dl" }
