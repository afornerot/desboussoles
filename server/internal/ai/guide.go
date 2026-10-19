package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Guide gère l'indexation et la recherche dans guide.md via amoxtli.
type Guide struct {
	binPath   string
	workspace string
	log       *slog.Logger
	ready     bool
}

// NewGuide crée le gestionnaire du guide et indexe guide.md si nécessaire.
func NewGuide(binPath, workspace, guidePath string, log *slog.Logger) *Guide {
	g := &Guide{
		binPath:   binPath,
		workspace: workspace,
		log:       log,
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		log.Error("guide workspace", "err", err)
		return g
	}
	if err := g.index(guidePath); err != nil {
		log.Error("guide indexation", "err", err)
		return g
	}
	g.ready = true
	log.Info("guide indexé", "workspace", workspace, "file", guidePath)
	return g
}

// Ready indique si l'index est utilisable.
func (g *Guide) Ready() bool { return g.ready }

// index initialise le workspace amoxtli et indexe le fichier guide.
func (g *Guide) index(guidePath string) error {
	// Init si pas déjà fait
	if _, err := os.Stat(filepath.Join(g.workspace, ".amoxtli")); os.IsNotExist(err) {
		if out, err := g.run("init"); err != nil {
			return fmt.Errorf("amoxtli init: %s (%s)", err, out)
		}
	}

	// Indexer le fichier (add est idempotent, met à jour si modifié)
	out, err := g.run("add", guidePath, "--json")
	if err != nil {
		return fmt.Errorf("amoxtli add: %s (%s)", err, out)
	}

	// Vérifier le statut
	var results []struct {
		File   string `json:"file"`
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(out), &results) == nil {
		for _, r := range results {
			g.log.Info("guide fichier indexé", "file", filepath.Base(r.File), "status", r.Status)
		}
	}
	return nil
}

// Search interroge l'index amoxtli et retourne les sections pertinentes.
// La sortie est bornée par maxChars (issu du budget provider) ainsi que par
// des bornes structurelles (sections par source, sections totales).
func (g *Guide) Search(ctx context.Context, query string, limit, maxChars int) (string, error) {
	if !g.ready {
		return "", fmt.Errorf("guide non indexé")
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	if maxChars <= 0 {
		maxChars = maxGuideResultLen
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := g.runCtx(cctx, "search", query, "--json", "-n", fmt.Sprintf("%d", limit))
	if err != nil {
		return "", fmt.Errorf("amoxtli search: %s", err)
	}

	// Parser le JSON et formater les résultats
	var resp struct {
		Results []struct {
			Source   string  `json:"source"`
			Score    float64 `json:"score"`
			Sections []struct {
				Content string `json:"content"`
			} `json:"sections"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		if len(out) > maxChars {
			out = out[:maxChars] + "…"
		}
		return out, nil // retourner le brut si parse échoue
	}

	var buf bytes.Buffer
	total := 0
	for _, r := range resp.Results {
		for i, s := range r.Sections {
			if i >= maxSectionsPerSource || total >= maxTotalSections {
				break
			}
			content := s.Content
			if len(content) > maxSectionLen {
				content = content[:maxSectionLen] + "…"
			}
			if buf.Len()+len(content) > maxChars {
				if buf.Len() == 0 {
					buf.WriteString(content[:maxChars])
				}
				buf.WriteString("\n\n… (résultats tronqués — affine la recherche)")
				return buf.String(), nil
			}
			buf.WriteString(content)
			buf.WriteString("\n\n---\n\n")
			total++
		}
	}
	result := buf.String()
	if result == "" {
		return "Aucun résultat trouvé dans le guide.", nil
	}
	return result, nil
}

// Bornes de sortie de la recherche guide.
const (
	maxSectionsPerSource = 3
	maxTotalSections     = 8
	maxSectionLen        = 1500
	maxGuideResultLen    = 4500
)

// run exécute amoxtli avec le workspace configuré.
func (g *Guide) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return g.runCtx(ctx, args...)
}

func (g *Guide) runCtx(ctx context.Context, args ...string) (string, error) {
	fullArgs := append([]string{"-C", g.workspace}, args...)
	cmd := exec.CommandContext(ctx, g.binPath, fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if stderr.Len() > 0 {
			return stdout.String(), fmt.Errorf("%s: %s", err, stderr.String())
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}
