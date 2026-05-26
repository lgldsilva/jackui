package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
	"github.com/luizg/jackui/internal/handlers"
	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/ui"
)

func main() {
	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	jackettClient := jackett.New(cfg.Jackett.URL, cfg.Jackett.APIKey)

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// CORS for dev frontend (Vite runs on 5173)
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{
		"http://localhost:5173",
		"http://localhost:3000",
		fmt.Sprintf("http://localhost:%d", cfg.Port),
	}
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	router.Use(cors.New(corsConfig))

	// API routes
	api := router.Group("/api")
	{
		api.GET("/search", handlers.Search(jackettClient))
		api.GET("/indexers", handlers.GetIndexers(jackettClient))
		api.POST("/download", handlers.Download(cfg))
		api.GET("/clients", handlers.GetClients(cfg))
		api.GET("/config", handlers.GetConfig(cfg, configPath))
		api.PUT("/config", handlers.UpdateConfig(cfg, configPath))
		api.POST("/config/test", handlers.TestJackett(cfg))
	}

	// Serve embedded frontend (dist/ inside the ui package)
	distFS, err := fs.Sub(ui.FS, "dist")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}

	fileServer := http.FileServer(http.FS(distFS))

	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// If it's an API call that didn't match, return 404 JSON
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "endpoint not found"})
			return
		}

		// Check if the file exists in dist
		f, err := distFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		// Fallback: serve index.html for SPA routing
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("JackUI starting on http://localhost%s", addr)

	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
