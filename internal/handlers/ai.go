package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/luizg/jackui/internal/ai"
)

// aiStatusResponse is the read model for the Settings AI card.
type aiStatusResponse struct {
	Enabled bool              `json:"enabled"`
	Chain   []aiSlotView      `json:"chain"`
	Results []ai.SlotScore    `json:"results"`
	Cases   []ai.BenchmarkCase `json:"cases"`
}

type aiSlotView struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// GetAIBenchmark — GET /api/ai/benchmark. Returns the live chain order, the last
// benchmark results, and the editable case set.
func GetAIBenchmark(client *ai.Client, store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp := aiStatusResponse{Enabled: client != nil}
		if client != nil {
			for _, s := range client.Slots() {
				resp.Chain = append(resp.Chain, aiSlotView{ID: s.ID, Provider: s.Provider, Model: s.Model})
			}
		}
		if store != nil {
			resp.Results = store.Results()
			resp.Cases = store.Cases()
		}
		c.JSON(http.StatusOK, resp)
	}
}

// RunAIBenchmark — POST /api/ai/benchmark. Runs every chain slot against the
// stored case set, persists the scores, and re-orders the live chain best-first.
// Synchronous: the caller waits (it hits external LLMs once per case×slot).
func RunAIBenchmark(client *ai.Client, store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI chain disabled"})
			return
		}
		var cases []ai.BenchmarkCase
		if store != nil {
			cases = store.Cases()
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()

		scores := client.Run(ctx, cases)
		if store != nil {
			if err := store.SaveResults(scores); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		// Apply the new ranking to the live chain so resolves use it immediately.
		order := make([]string, len(scores))
		for i, s := range scores {
			order[i] = s.SlotID
		}
		client.ApplyOrder(order)

		c.JSON(http.StatusOK, gin.H{"results": scores})
	}
}

type aiCasesReq struct {
	Cases []ai.BenchmarkCase `json:"cases"`
}

// PutAICases — PUT /api/ai/benchmark/cases. Replaces the whole editable case set.
func PutAICases(store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "benchmark store unavailable"})
			return
		}
		var req aiCasesReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := store.SetCases(req.Cases); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"cases": store.Cases()})
	}
}
