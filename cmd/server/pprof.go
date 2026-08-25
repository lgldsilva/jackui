package main

import (
	"crypto/subtle"
	"log"
	"net/http/pprof"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

// pprofEnabled diz se o /debug/pprof deve ser exposto. Opt-in (default OFF):
// os profiles carregam heap/goroutines, ou seja, nomes de torrent, caminhos de
// arquivo e tokens em voo.
func pprofEnabled() bool {
	v := os.Getenv("JACKUI_PPROF_ENABLED")
	return v == "1" || v == "true"
}

// staticTokenGuard aborta com 401 quando o token estático não confere. Aceita
// `Authorization: Bearer <token>` ou `?token=` — `go tool pprof <url>` não tem
// como mandar header, então a query é o caminho realista. Compare constante.
func staticTokenGuard(static string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if subtle.ConstantTimeCompare([]byte(presentedToken(c)), []byte(static)) != 1 {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// presentedToken extrai o token do header Bearer ou, na ausência dele, da query.
func presentedToken(c *gin.Context) string {
	authz := c.GetHeader("Authorization")
	presented := strings.TrimPrefix(authz, "Bearer ")
	if presented == "" || presented == authz {
		presented = c.Query("token")
	}
	return presented
}

// registerPprofRoutes expõe net/http/pprof sob /debug/pprof, sempre autenticado.
//
// Dois modos, nenhum deles anônimo:
//   - JACKUI_PPROF_TOKEN definido → token estático (header ou ?token=), que é o
//     único jeito de o `go tool pprof` chegar no endpoint sem browser.
//   - sem token, mas com auth JWT ligada → exige JWT de admin.
//
// Sem token E sem auth não há identidade nenhuma para checar, então as rotas
// NÃO são registradas (log explícito) em vez de abrirem um dump de memória para
// a rede inteira. Devolve true quando registrou.
func registerPprofRoutes(router gin.IRouter, deps *appDeps) bool {
	if !pprofEnabled() {
		return false
	}

	static := strings.TrimSpace(os.Getenv("JACKUI_PPROF_TOKEN"))
	jwtAvailable := deps != nil && deps.cfg != nil && deps.cfg.Auth.Enabled && deps.tokenMgr != nil

	var guards []gin.HandlerFunc
	switch {
	case static != "":
		guards = append(guards, staticTokenGuard(static))
	case jwtAvailable:
		guards = append(guards, auth.Required(deps.tokenMgr), auth.AdminOnly())
	default:
		log.Printf("pprof: JACKUI_PPROF_ENABLED está ligado mas não há como autenticar " +
			"(defina JACKUI_PPROF_TOKEN ou habilite a auth JWT) — /debug/pprof NÃO foi exposto")
		return false
	}

	group := router.Group("/debug/pprof", guards...)
	group.GET("/", gin.WrapF(pprof.Index))
	group.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	group.GET("/profile", gin.WrapF(pprof.Profile))
	group.GET("/trace", gin.WrapF(pprof.Trace))
	group.GET("/symbol", gin.WrapF(pprof.Symbol))
	group.POST("/symbol", gin.WrapF(pprof.Symbol))
	// Perfis nomeados, explícitos: um wildcard :profile colidiria com as rotas
	// estáticas acima na mesma posição da árvore do gin.
	for _, name := range []string{"heap", "goroutine", "allocs", "block", "mutex", "threadcreate"} {
		group.GET("/"+name, gin.WrapH(pprof.Handler(name)))
	}
	return true
}
