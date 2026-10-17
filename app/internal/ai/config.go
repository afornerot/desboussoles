// Package ai implémente le chat IA de Boussole sur le SDK genai.
// Port de la logique de l'agent ninecase, adaptée : streaming SSE,
// outils lexique + carte (PostGIS), sessions en mémoire.
package ai

import (
	"bufio"
	"os"
	"strings"
)

// LoadEnv charge .env.local puis .env dans cet ordre, sans écraser
// les variables déjà présentes dans l'environnement.
func LoadEnv(dir string) error {
	for _, name := range []string{".env.local", ".env"} {
		if err := loadEnvFile(dir + "/" + name); err != nil {
			return err
		}
	}
	return nil
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

// MapToGenAI propage AI_* vers les variables du SDK genai.
// Provider OpenAI-compatible (xolo, gemini via openai compat, deepseek…).
func MapToGenAI() {
	provider := os.Getenv("AI_PROVIDER")
	setIfAbsent("GENAI_CHAT_COMPLETION_PROVIDER", "openai")
	setIfAbsent("GENAI_CHAT_COMPLETION_OPENAI_MODEL", os.Getenv("AI_MODEL"))
	setIfAbsent("GENAI_CHAT_COMPLETION_OPENAI_API_KEY", os.Getenv("AI_API_KEY"))
	setIfAbsent("GENAI_CHAT_COMPLETION_OPENAI_BASE_URL", os.Getenv("AI_BASE_URL"))
	_ = provider // AI_PROVIDER documente le provider choisi, le SDK est openai-compatible
}

func setIfAbsent(key, value string) {
	if value == "" {
		return
	}
	if _, exists := os.LookupEnv(key); !exists {
		os.Setenv(key, value)
	}
}

// IsConfigured indique si les variables AI minimales sont présentes.
func IsConfigured() bool {
	return os.Getenv("AI_MODEL") != "" && os.Getenv("AI_API_KEY") != "" && os.Getenv("AI_BASE_URL") != ""
}
