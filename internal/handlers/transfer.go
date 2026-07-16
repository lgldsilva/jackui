package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/transfer"
)

// TransfersList handles GET /api/transfers — the single polling endpoint behind
// the global Transfers dock. Non-admins see only their own jobs (+ system
// userID=0); admins see every job so they can diagnose stuck promotes.
func TransfersList(tr *transfer.Tracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		list := tr.List(userID, isAdmin)
		if list == nil {
			list = []transfer.Snapshot{}
		}
		c.JSON(http.StatusOK, gin.H{"transfers": list})
	}
}

// TransfersCancel handles DELETE /api/transfers/:id — cancels an in-flight
// move/copy job: its context is canceled so the producer (post-download move,
// promote, Local move) aborts its copy/retries, and the job flips to "canceled".
// 404 when no job with that ID exists (or the job belongs to another user).
func TransfersCancel(tr *transfer.Tracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		if id == "" || !tr.Cancel(id, userID, isAdmin) {
			c.JSON(http.StatusNotFound, gin.H{"error": "transferência não encontrada"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
