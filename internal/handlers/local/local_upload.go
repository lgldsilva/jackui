package local

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	lb "github.com/lgldsilva/jackui/internal/local"
)

// Upload de arquivos locais — extraído de local.go.
func LocalUpload(b *lb.Browser, maxUploadBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")

		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mount is required"})
			return
		}

		if !canModifyMount(c, mount) {
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}

		// Teto de tamanho (anti disk-fill): MaxBytesReader corta a leitura do
		// corpo inteiro (multipart incluso) antes de escrever no disco.
		if maxUploadBytes > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
		}

		fileHeader, filename, ok := validateUpload(c, maxUploadBytes)
		if !ok {
			return
		}

		absDir, absPath, ok := resolveUploadDest(c, b, mount, path, filename)
		if !ok {
			return
		}

		finalName, ok := streamUploadToDisk(c, fileHeader, absDir, absPath, filename)
		if !ok {
			return
		}
		c.JSON(http.StatusCreated, gin.H{"uploaded": finalName, "path": filepath.Join(path, finalName)})
	}
}

// streamUploadToDisk abre o arquivo enviado, garante o diretório de destino e
// grava em disco com claim atômico (createUploadFile faz o auto-rename em
// colisão). Em erro responde o JSON apropriado e retorna ok=false.
func streamUploadToDisk(c *gin.Context, fileHeader *multipart.FileHeader, absDir, absPath, filename string) (string, bool) {
	srcFile, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao abrir arquivo enviado: " + err.Error()})
		return "", false
	}
	defer srcFile.Close()

	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao criar diretório: " + err.Error()})
		return "", false
	}

	dstFile, finalPath, ok := createUploadFile(c, absDir, absPath, filename)
	if !ok {
		return "", false
	}
	defer dstFile.Close()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		_ = os.Remove(finalPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao gravar arquivo: " + err.Error()})
		return "", false
	}
	return filepath.Base(finalPath), true
}

// validateUpload pulls the "file" part, validates its name and extension, and
// enforces the size ceiling. It writes the JSON error and returns ok=false on
// any failure; on success returns the header and the sanitized base filename.
func validateUpload(c *gin.Context, maxUploadBytes int64) (fileHeader *multipart.FileHeader, filename string, ok bool) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required: " + err.Error()})
		return nil, "", false
	}

	filename = filepath.Base(fileHeader.Filename)
	if filename == "" || filename == "." || filename == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return nil, "", false
	}

	if !allowedUploadExts[strings.ToLower(filepath.Ext(filename))] {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "tipo de arquivo não permitido (apenas vídeo/legenda)"})
		return nil, "", false
	}

	// Rejeição amigável e barata antes de ler o corpo (o MaxBytesReader
	// acima é a garantia dura; isto evita gravar parcial p/ um Size já grande).
	if maxUploadBytes > 0 && fileHeader.Size > maxUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("arquivo excede o limite de %d MB", maxUploadBytes/(1<<20))})
		return nil, "", false
	}

	return fileHeader, filename, true
}

// resolveUploadDest resolves the user-scoped destination directory and the
// target file path, guarding against path traversal. Writes the JSON error and
// returns ok=false on failure.
func resolveUploadDest(c *gin.Context, b *lb.Browser, mount, path, filename string) (absDir, absPath string, ok bool) {
	scoped := b.UserScopedPath(mount, path, scopeUser(c))
	absDir, err := b.ResolvePath(mount, scoped)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "caminho de destino inválido: " + err.Error()})
		return "", "", false
	}

	absPath = filepath.Join(absDir, filename)
	if !strings.HasPrefix(absPath, absDir) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path traversal detectado"})
		return "", "", false
	}

	return absDir, absPath, true
}

// createUploadFile creates the destination file, auto-renaming on collision so
// one user never clobbers another's file (the destination dir may be shared).
// O_EXCL makes claim+create atomic, so two concurrent uploads of the same name
// resolve to distinct files ("foo.mkv" → "foo (1).mkv" → ...) instead of one
// overwriting the other. Writes the JSON error and returns ok=false on failure.
func createUploadFile(c *gin.Context, absDir, absPath, filename string) (dstFile *os.File, finalPath string, ok bool) {
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	finalPath = absPath
	for i := 1; ; i++ {
		// #nosec G304 G302 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna; arquivo de midia; 0644 intencional p/ leitura
		f, err := os.OpenFile(finalPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if err == nil {
			return f, finalPath, true
		}
		if !os.IsExist(err) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "erro ao criar arquivo no servidor: " + err.Error()})
			return nil, "", false
		}
		if i > 9999 {
			c.JSON(http.StatusConflict, gin.H{"error": "muitos arquivos com o mesmo nome neste diretório"})
			return nil, "", false
		}
		finalPath = filepath.Join(absDir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

type moveEntryReq struct {
	SrcMount string `json:"srcMount"`
	SrcPath  string `json:"srcPath"`
	DstMount string `json:"dstMount"`
	DstPath  string `json:"dstPath"`
}
