package main

import (
	"context"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/lgldsilva/jackui/internal/auth"
	appdb "github.com/lgldsilva/jackui/internal/db"
)

// initDB opens the shared PostgreSQL pool and applies the unified schema. All
// stores receive this pool. Fatal if DATABASE_URL is unset or unreachable.
func initDB(deps *appDeps) {
	if deps.cfg.DatabaseURL == "" {
		log.Fatalf("DATABASE_URL ausente — defina JACKUI_DATABASE_URL (ou DATABASE_URL / JACKUI_PG_*) com o DSN do PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := appdb.Open(ctx, deps.cfg.DatabaseURL, 60*time.Second)
	if err != nil {
		cancel()
		log.Fatalf("PostgreSQL init failed: %v", err)
	}
	if err := appdb.Migrate(pool); err != nil {
		cancel()
		log.Fatalf("PostgreSQL migrate failed: %v", err)
	}
	deps.db = pool
	deps.addCleanup(func() { _ = pool.Close() })
	log.Printf("PostgreSQL pool ready; schema migrated")
}

func initAuth(deps *appDeps) {
	deps.loginLockout = auth.NewLockout(5, 15*time.Minute)
	deps.loginRateLimiter = auth.NewIPRateLimiter(10, 1*time.Minute)
	deps.registerRateLimiter = auth.NewIPRateLimiter(5, 1*time.Minute)
	deps.passwordRateLimiter = auth.NewIPRateLimiter(3, 1*time.Minute)
	if !deps.cfg.Auth.Enabled {
		if os.Getenv("JACKUI_ALLOW_INSECURE_AUTH") != "1" {
			log.Fatalf("Auth disabled — set JACKUI_AUTH_ENABLED=1 (recommended) or JACKUI_ALLOW_INSECURE_AUTH=1 for dev/LAN only")
		}
		log.Printf("WARNING: auth disabled with JACKUI_ALLOW_INSECURE_AUTH=1 — ALL endpoints are public, including admin routes (config, mounts, cache) and the Transmission RPC. Only run like this behind a trusted reverse proxy / on a private LAN.")
		return
	}
	initAuthStore(deps)
	initJWTSecret(deps)
	initPasskeys(deps)
	bootstrapAdmin(deps)
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			// #nosec G104 -- limpeza periodica best-effort em background
			deps.authStore.CleanupExpired()
		}
	}()
}

func initAuthStore(deps *appDeps) {
	authStore, err := auth.New(deps.db)
	if err != nil {
		log.Fatalf("Auth store init failed: %v", err)
	}
	deps.authStore = authStore
	log.Printf("Auth enabled: user store on PostgreSQL")
}

func initJWTSecret(deps *appDeps) {
	secret := []byte(deps.cfg.Auth.JWTSecret)
	// Auth is enabled here (initJWTSecret only runs from initAuth). A missing/
	// short secret used to fall back to a random one per boot — which silently
	// invalidated every session on each restart (refresh tokens, MFA flows). Fail
	// fast and demand a persistent secret instead of degrading auth silently.
	if len(secret) < 32 {
		log.Fatalf("Auth: jwt_secret ausente ou curto (%d bytes) — defina jwt_secret no config ou JACKUI_JWT_SECRET com pelo menos 32 bytes; um secret efêmero desloga todas as sessões a cada restart", len(secret))
	}
	deps.tokenMgr = auth.NewTokenManager(secret, 15*time.Minute)
}

func initPasskeys(deps *appDeps) {
	if deps.cfg.BaseURL == "" {
		log.Printf("Passkeys (WebAuthn): disabled — set JACKUI_BASE_URL to the public https origin to enable")
		return
	}
	u, perr := url.Parse(deps.cfg.BaseURL)
	if perr != nil || u.Host == "" {
		log.Printf("Passkeys (WebAuthn): disabled — set JACKUI_BASE_URL to the public https origin to enable")
		return
	}
	origin := u.Scheme + "://" + u.Host
	wm, werr := auth.NewWAManager(u.Hostname(), "JackUI", origin)
	if werr != nil {
		log.Printf("Passkeys: disabled — %v", werr)
		return
	}
	deps.waManager = wm
	log.Printf("Passkeys (WebAuthn): enabled for %s (RPID=%s)", origin, u.Hostname())
}

func bootstrapAdmin(deps *appDeps) {
	adminUser := deps.cfg.Auth.AdminUsername
	if adminUser == "" {
		adminUser = "admin"
	}
	if deps.cfg.Auth.AdminPassword == "" {
		log.Fatalf("Auth enabled but JACKUI_ADMIN_PASSWORD / config admin_password not set")
	}
	if err := deps.authStore.Bootstrap(adminUser, deps.cfg.Auth.AdminPassword); err != nil {
		log.Fatalf("Auth bootstrap failed: %v", err)
	}
	log.Printf("Admin user=%s", adminUser)
}
