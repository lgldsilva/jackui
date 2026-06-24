package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/transfer"
)

// TransfersList handles GET /api/transfers — the single polling endpoint behind
// the global Transfers dock. It returns every active and recently-finished
// move/copy job (post-download move, Local-tab move, promote/AI-rename), newest
// first, each with X/Y files, bytes done/total, transfer rate and ETA. The dock
// polls this while any job is running and hides itself when the list is empty.
func TransfersList(tr *transfer.Tracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		list := tr.List()
		if list == nil {
			list = []transfer.Snapshot{}
		}
		c.JSON(http.StatusOK, gin.H{"transfers": list})
	}
}

// TransfersCancel handles DELETE /api/transfers/:id — cancels an in-flight
// move/copy job: its context is canceled so the producer (post-download move,
// promote, Local move) aborts its copy/retries, and the job flips to "canceled".
// 404 when no job with that ID exists. This is what the dock's "stop" button calls
// — previously the button rendered but had nothing to invoke.
func TransfersCancel(tr *transfer.Tracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == "" || !tr.Cancel(id) {
			c.JSON(http.StatusNotFound, gin.H{"error": "transferência não encontrada"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
