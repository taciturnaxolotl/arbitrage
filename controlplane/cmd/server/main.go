// @title           Control Plane API
// @version          1.0
// @description     Remote client management and command dispatch API with live dashboard.
// @host             localhost:8080
// @BasePath         /
// @securityDefinitions.basic ClientAuth
// @in header
// @name Authorization
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/docs"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/handlers"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/middleware"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/store"
	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/ws"
)

//go:embed static/*
var staticFS embed.FS

func main() {
	godotenv.Load()

	indikoCfg := middleware.LoadIndikoConfigFromEnv()
	if indikoCfg == nil {
		log.Fatal("Missing Indiko OAuth2 configuration. Set INDIKO_ISSUER, INDIKO_CLIENT_ID, INDIKO_CLIENT_SECRET environment variables.")
	}

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		log.Fatal("SESSION_SECRET environment variable is required")
	}

	dbPath := os.Getenv("DB_PATH")
	s := store.New(dbPath)
	h := ws.NewHub(s)
	api := handlers.NewAPI(s, h)

	auth := middleware.NewAuth(indikoCfg, sessionSecret, s)
	api.SetSessionStore(auth.SessionStore())

	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	r.Get("/auth/login", auth.LoginHandler)
	r.Get("/auth/callback", auth.CallbackHandler)
	r.Get("/auth/logout", auth.LogoutHandler)
	r.Get("/auth/denied", auth.DeniedHandler)

	api.RegisterRoutes(r)

	r.Get("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))
	r.Get("/swagger/doc.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(docs.SwaggerInfo.ReadDoc()))
	})

	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("failed to get static fs: %v", err)
	}
	r.Handle("/*", auth.RequireAuth(http.FileServer(http.FS(staticContent))))
	r.Get("/ws", auth.RequireAuth(http.HandlerFunc(h.HandleWebSocket)).ServeHTTP)
	r.Get("/api/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Client WS requires Basic Auth
		id, token, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="controlplane"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !s.AuthenticateClient(id, token) {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		h.HandleClientWebSocket(w, r, id)
	}))

	go s.StaleChecker()

	addr := ":8080"
	fmt.Printf("Control plane server starting on %s\n", addr)
	fmt.Printf("Indiko OAuth2:  %s\n", indikoCfg.Issuer)
	fmt.Printf("Client ID:      %s\n", indikoCfg.ClientID)
	fmt.Printf("Dashboard:      http://localhost%s\n", addr)
	fmt.Printf("API Docs:       http://localhost%s/swagger/index.html\n", addr)
	fmt.Printf("WebSocket:      ws://localhost%s/ws\n", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
