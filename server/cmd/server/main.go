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

	// Trace JSONL des tours de chat (analyse a posteriori) :
	// AI_TRACE_FILE change le chemin, AI_TRACE_FILE=none désactive.
	tracePath := os.Getenv("AI_TRACE_FILE")
	if tracePath == "" {
		tracePath = "/data/traces/chat.jsonl"
	}
	if tracePath != "none" {
		if err := ai.SetTraceFile(tracePath); err != nil {
			log.Warn("fichier traces", "path", tracePath, "err", err)
		} else {
			log.Info("traces chat activées", "path", tracePath)
		}
	}

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

		switch os.Getenv("AI_PROVIDER") {
		case "opencode":
			// Client custom pour OpenCode Go : gère x-opencode-session + SSE manuellement
			// (le SDK openai-go a des problèmes de décodage avec les champs reasoning de mimo)
			client = ai.NewOpenCodeClient(
				os.Getenv("AI_BASE_URL"),
				os.Getenv("AI_API_KEY"),
				log,
			)
			log.Info("client OpenCode Go actif", "model", os.Getenv("AI_MODEL"))
		case "gemini", "groq":
			// Client custom pour les endpoints OpenAI-compatible : rejoue les
			// thought_signature des function calls de Gemini (le SDK les perd,
			// Gemini refuse alors la requête suivante). Sans effet ailleurs.
			client = ai.NewOpenAICompatClient(
				os.Getenv("AI_BASE_URL"),
				os.Getenv("AI_API_KEY"),
				log,
			)
			log.Info("client OpenAI-compat actif",
				"provider", os.Getenv("AI_PROVIDER"),
				"model", os.Getenv("AI_MODEL"))
		default:
			var err error
			client, err = provider.Create(ctx, env.With("GENAI_"))
			if err != nil {
				log.Error("client LLM", "err", err)
			} else {
				client = retry.NewClient(client, time.Second, 3)
			}
		}

		if client != nil {
			// Budget LLM : bornes dérivées des limites réelles du provider
			// (sonde x-ratelimit-*, profils, variables AI_MAX_REQUEST_TOKENS /
			// AI_REQUESTS_PER_DAY) plutôt qu'en dur.
			limits := ai.DetectLimits(ctx,
				os.Getenv("AI_PROVIDER"),
				os.Getenv("AI_BASE_URL"),
				os.Getenv("AI_API_KEY"),
				os.Getenv("AI_MODEL"),
				log)
			budget := ai.NewBudget(limits)
			log.Info("budget LLM adapté",
				"source", budget.Source,
				"max_request_tokens", budget.MaxRequestTokens,
				"requests_per_day", budget.RequestsPerDay,
				"history_chars", budget.MaxHistoryChars,
				"tool_result_chars", budget.MaxToolResultChars,
				"iterations", budget.MaxIterations)

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

			toolSet := ai.NewTools(st, guide, budget)
			svc := ai.New(client, toolSet.All(), ai.Config{
				MaxIterations: budget.MaxIterations,
				CallTimeout:   90 * time.Second,
				Budget:        budget,
			}, log)
			chatDeps = web.ChatDeps{
				Service:        svc,
				Sessions:       ai.NewSessionStore(time.Hour, budget.MaxHistoryChars),
				PublicSessions: ai.NewSessionStore(time.Hour, budget.MaxHistoryChars),
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
