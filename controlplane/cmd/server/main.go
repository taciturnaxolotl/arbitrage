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

	auth := middleware.NewAuth(indikoCfg, sessionSecret)

	dbPath := os.Getenv("DB_PATH")
	s := store.New(dbPath)
	h := ws.NewHub()
	api := handlers.NewAPI(s, h)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /auth/login", auth.LoginHandler)
	mux.HandleFunc("GET /auth/callback", auth.CallbackHandler)
	mux.HandleFunc("GET /auth/logout", auth.LogoutHandler)

	api.RegisterRoutes(mux)

	mux.HandleFunc("GET /swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))
	mux.HandleFunc("GET /swagger/doc.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(docs.SwaggerInfo.ReadDoc()))
	})

	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("failed to get static fs: %v", err)
	}
	mux.Handle("/", auth.RequireAuth(http.FileServer(http.FS(staticContent))))
	mux.Handle("/ws", auth.RequireAuth(http.HandlerFunc(h.HandleWebSocket)))

	go s.StaleChecker()

	handler := middleware.CORS(mux)

	addr := ":8080"
	fmt.Printf("Control plane server starting on %s\n", addr)
	fmt.Printf("Indiko OAuth2:  %s\n", indikoCfg.Issuer)
	fmt.Printf("Client ID:      %s\n", indikoCfg.ClientID)
	fmt.Printf("Dashboard:      http://localhost%s\n", addr)
	fmt.Printf("API Docs:       http://localhost%s/swagger/index.html\n", addr)
	fmt.Printf("WebSocket:      ws://localhost%s/ws\n", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
