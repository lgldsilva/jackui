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
