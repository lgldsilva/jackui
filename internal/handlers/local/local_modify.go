package local

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func LocalDelete(b *lb.Browser, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		if !canModifyMount(c, mount) {
			return
		}
		abs, err := resolveDeletablePath(b, mount, ScopePath(b, c, mount, path))
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": errFileOrDirNotFound})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Find the torrent(s) linked to this path BEFORE deleting it (the lookup
		// matches on the on-disk file_path). Deleting a local file/folder must
		// also tear down its torrent so it doesn't linger in Downloads, in the
		// piece cache or as a favorite.
		linked, _ := dls.FindByPathPrefix(abs)
		if err := os.RemoveAll(abs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete: %s", err.Error())})
			return
		}
		removed := purgeLinkedTorrents(dls, s, linked)
		c.JSON(http.StatusOK, gin.H{"message": "deleted successfully", "torrentsRemoved": removed})
	}
}

// LocalCleanEmptyDirs handles POST /api/local/clean-empty — removes empty
// subdirectories under the given path (or the mount root when path is empty).
// Same access model as delete: writable mount ("meus downloads") or admin only.
// Only removes truly-empty dirs, never the starting dir or the mount root.
func LocalCleanEmptyDirs(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		if !canModifyMount(c, mount) {
			return
		}
		cleaned, err := b.RemoveEmptyDirs(mount, ScopePath(b, c, mount, c.Query("path")))
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"cleaned": cleaned})
	}
}

type folderLockReq struct {
	Mount  string `json:"mount"`
	Path   string `json:"path"`
	Locked bool   `json:"locked"`
}

// LocalSetFolderLock handles POST /api/local/lock — pins/unpins a folder so the
// "clean empty folders" sweep keeps it even with no files inside (a ".keep"
// marker). Same access model as delete/clean: a writable mount ("meus
// downloads") or admin. The mount root can't be pinned.
func LocalSetFolderLock(b *lb.Browser, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := bindFolderLockReq(c)
		if !ok {
			return
		}
		if AbortIfLocalPathHidden(c, s, req.Mount, req.Path) {
			return
		}
		if !CheckMountAccess(b, c, req.Mount) || !canModifyMount(c, req.Mount) {
			return
		}
		if err := b.SetFolderLock(req.Mount, ScopePath(b, c, req.Mount, req.Path), req.Locked); err != nil {
			respondFolderLockErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"locked": req.Locked})
	}
}

func bindFolderLockReq(c *gin.Context) (folderLockReq, bool) {
	var req folderLockReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return req, false
	}
	if req.Mount == "" || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
		return req, false
	}
	return req, true
}

func respondFolderLockErr(c *gin.Context, err error) {
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func canModifyMount(c *gin.Context, mount string) bool {
	claims, _ := auth.ClaimsFromCtx(c)
	isAdmin := claims != nil && claims.Role == auth.RoleAdmin
	if isAdmin || strings.ToLower(mount) == mountMeusDownloads {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": errOnlyMeusDownloads})
	return false
}

func resolveDeletablePath(b *lb.Browser, mount, path string) (string, error) {
	cleanPath := filepath.Clean(path)
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
		return "", fmt.Errorf("cannot delete mount root")
	}
	abs, err := b.ResolvePath(mount, path)
	if err != nil {
		return "", err
	}
	if isMountRoot(b, abs) {
		return "", fmt.Errorf("cannot delete mount root")
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	return abs, nil
}

func isMountRoot(b *lb.Browser, abs string) bool {
	for _, m := range b.Mounts() {
		mountAbs, err := filepath.Abs(m.Path)
		if err == nil && abs == mountAbs {
			return true
		}
	}
	return false
}
