package handlers

import (
	"net/http"
	"strconv"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
)

// respondError writes the project's standard JSON error body: {"error": "..."}.
// Use it instead of inlining c.JSON(status, gin.H{"error": ...}) so the shape
// stays uniform (there were two competing formats before this).
func respondError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": err.Error()})
}

// bindHash reads the ":hash" path param, parses it into a metainfo.Hash and, on
// failure, writes the standard 400 and returns ok=false — so the caller can just
// `if !ok { return }`. Collapses the parseHash+400 block repeated across stream.go.
func bindHash(c *gin.Context) (metainfo.Hash, bool) {
	h, err := parseHash(c.Param("hash"))
	if err != nil {
		respondError(c, http.StatusBadRequest, err)
		return h, false
	}
	return h, true
}

// bindFileIndex reads an integer path param (e.g. "file"), writing the standard
// 400 with the shared errInvalidFileIndex message on a parse error and returning
// ok=false. Collapses the strconv.Atoi+400 block repeated across stream.go.
func bindFileIndex(c *gin.Context, name string) (int, bool) {
	v, err := strconv.Atoi(c.Param(name))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
		return 0, false
	}
	return v, true
}

// queryBool parses a query parameter as a boolean accepting any strconv.ParseBool
// form (1/t/T/true/TRUE, 0/f/false, ...). Missing or unparseable yields false.
// Fixes handlers that only accepted "1" so an external client passing ?flag=true
// no longer silently reads as false. The web UI already sends "1", so it's a
// strict superset of the previous behaviour.
func queryBool(c *gin.Context, key string) bool {
	b, _ := strconv.ParseBool(c.Query(key))
	return b
}
