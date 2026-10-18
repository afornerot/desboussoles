package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bornholm/genai/llm"
	"github.com/bornholm/genai/llm/provider"
	_ "github.com/bornholm/genai/llm/provider/all"
	"github.com/bornholm/genai/llm/provider/env"
	"github.com/bornholm/genai/llm/retry"

	"boussole/internal/ai"
	"boussole/internal/store"
	"boussole/internal/web"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("DEBUG") == "1" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://boussoule:changeme@localhost:5432/boussole?sslmode=disable"
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	mapboxToken := os.Getenv("MAPBOX_TOKEN")
	guideURL := os.Getenv("GUIDE_API_URL")

	// Environnement AI : .env.local > .env, puis mappage AI_* → GENAI_*.
	if err := ai.LoadEnv("."); err != nil {
		log.Warn("chargement .env", "err", err)
	}
	ai.MapToGenAI()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, dsn)
	if err != nil {
		log.Error("store init", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// Chat IA : initialisation du client LLM (optionnel, échec non fatal).
	chatDeps := web.ChatDeps{}
	if ai.IsConfigured() {
		var client llm.Client

		if os.Getenv("AI_PROVIDER") == "opencode" {
			// Client custom pour OpenCode Go : gère x-opencode-session + SSE manuellement
			// (le SDK openai-go a des problèmes de décodage avec les champs reasoning de mimo)
			client = ai.NewOpenCodeClient(
				os.Getenv("AI_BASE_URL"),
				os.Getenv("AI_API_KEY"),
				log,
			)
			log.Info("client OpenCode Go actif", "model", os.Getenv("AI_MODEL"))
		} else {
			var err error
			client, err = provider.Create(ctx, env.With("GENAI_"))
			if err != nil {
				log.Error("client LLM", "err", err)
			} else {
				client = retry.NewClient(client, time.Second, 3)
			}
		}

		if client != nil {
			// Indexation du guide via amoxtli
			guideBin := os.Getenv("AMOXTLI_BIN")
			if guideBin == "" {
				guideBin = "amoxtli"
			}
			guideDir := os.Getenv("AMOXTLI_DIR")
			if guideDir == "" {
				guideDir = "/data/amoxtli"
			}
			guideFile := os.Getenv("GUIDE_FILE")
			if guideFile == "" {
				guideFile = "/data/guide.md"
			}
			var guide *ai.Guide
			if _, err := os.Stat(guideFile); err == nil {
				guide = ai.NewGuide(guideBin, guideDir, guideFile, log)
			} else {
				log.Info("guide.md absent, outil guide_search désactivé", "path", guideFile)
			}

			toolSet := ai.NewTools(st, guide)
			svc := ai.New(client, toolSet.All(), ai.Config{
				MaxIterations: 4,
				CallTimeout:   90 * time.Second,
			}, log)
			chatDeps = web.ChatDeps{
				Service:        svc,
				Sessions:       ai.NewSessionStore(time.Hour),
				PublicSessions: ai.NewSessionStore(time.Hour),
				Ready:          true,
			}
			log.Info("chat IA activé",
				"model", os.Getenv("AI_MODEL"),
				"provider", os.Getenv("AI_PROVIDER"),
			)
		}
	} else {
		log.Info("chat IA désactivé (AI_* non configurées)")
	}

	srv, err := web.New(st, mapboxToken, guideURL, chatDeps, log)
	if err != nil {
		log.Error("web init", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
