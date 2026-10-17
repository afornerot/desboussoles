package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bornholm/genai/llm"

	"boussole/internal/store"
)

// Tools expose les outils LLM de Boussole (lexique + carte PostGIS + guide).
type Tools struct {
	store *store.Store
	guide *Guide
}

// NewTools construit l'ensemble des outils.
func NewTools(st *store.Store, guide *Guide) *Tools {
	return &Tools{store: st, guide: guide}
}

// All retourne la liste des outils exposés au modèle.
func (t *Tools) All() []llm.Tool {
	return []llm.Tool{
		llm.NewFuncTool(
			"lexique_search",
			"Recherche dans le lexique santé mentale (306 termes : définitions, acronymes, dispositifs). Utilise pour expliquer un terme ou trouver une définition.",
			llm.NewJSONSchema().
				RequiredProperty("query", "Terme ou question à rechercher", "string").
				Property("limit", "Nombre max de résultats (défaut 5)", "integer"),
			t.lexiqueSearch,
		),
		llm.NewFuncTool(
			"map_search",
			"Recherche des entités géographiques par nom sur la carte (établissements, cantons, villes, département du Doubs).",
			llm.NewJSONSchema().
				Property("query", "Nom ou partie de nom à rechercher", "string").
				Property("category", "Filtrer par catégorie : etablissement, canton, ville, departement", "string").
				Property("subcategories", "Filtrer par sous-catégories d'établissements (ex: urgences, ambulatoire, enfants-ados, hospitalisation, autisme…)", "array").
				Property("limit", "Nombre max de résultats (défaut 10)", "integer"),
			t.mapSearch,
		),
		llm.NewFuncTool(
			"map_nearby",
			"Trouve les entités proches d'un point géographique (coordonnées GPS) dans un rayon en km. Utile pour 'qu'est-ce qu'il y a près de X ?'.",
			llm.NewJSONSchema().
				RequiredProperty("lng", "Longitude (ex: 6.02 pour Besançon)", "number").
				RequiredProperty("lat", "Latitude (ex: 47.23 pour Besançon)", "number").
				Property("radius_km", "Rayon de recherche en km (défaut 10)", "number").
				Property("category", "Filtrer par catégorie : etablissement, canton, ville", "string").
				Property("subcategories", "Filtrer par sous-catégories d'établissements", "array").
				Property("limit", "Nombre max de résultats (défaut 10)", "integer"),
			t.mapNearby,
		),
		llm.NewFuncTool(
			"map_within",
			"Trouve les entités à l'intérieur d'une zone (canton, ville ou département du Doubs) par son nom.",
			llm.NewJSONSchema().
				RequiredProperty("zone", "Nom ou code de la zone (ex: Pontarlier, Besançon, 25)", "string").
				Property("category", "Filtrer par catégorie : etablissement, ville", "string").
				Property("subcategories", "Filtrer par sous-catégories d'établissements", "array").
				Property("limit", "Nombre max de résultats (défaut 10)", "integer"),
			t.mapWithin,
		),
		llm.NewFuncTool(
			"guide_search",
			"Recherche dans le Guide des partenaires en santé mentale du Doubs (établissements, structures, coordonnées, contacts). Utilise pour trouver des infos sur un dispositif, une structure, un professionnel, un numéro de téléphone.",
			llm.NewJSONSchema().
				RequiredProperty("query", "Question ou terme à rechercher dans le guide", "string").
				Property("limit", "Nombre max de sections (défaut 5)", "integer"),
			t.guideSearch,
		),
	}
}

func (t *Tools) lexiqueSearch(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
	query, err := paramString(params, "query")
	if err != nil {
		return errorResult(err.Error()), nil
	}
	limit := paramIntDefault(params, "limit", 5)

	terms, err := t.store.ListTerms(ctx, query, 0, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("erreur de recherche: %s", err)), nil
	}

	type item struct {
		Term       string   `json:"term"`
		Definition string   `json:"definition"`
		Sources    []string `json:"sources"`
	}
	results := make([]item, 0, len(terms))
	for _, trm := range terms {
		def := trm.Definition
		if len(def) > 500 {
			def = def[:500] + "…"
		}
		results = append(results, item{Term: trm.Term, Definition: def, Sources: trm.Sources})
	}
	return jsonResult(map[string]any{"count": len(results), "results": results}), nil
}

func (t *Tools) mapSearch(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
	query := paramStringDefault(params, "query", "")
	category := paramStringDefault(params, "category", "")
	subcats := paramStringSlice(params, "subcategories")
	limit := paramIntDefault(params, "limit", 10)

	feats, err := t.store.SearchMapFeatures(ctx, query, category, subcats, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("erreur de recherche carte: %s", err)), nil
	}
	return jsonResult(map[string]any{"count": len(feats), "results": mapFeatureResults(feats, nil)}), nil
}

func (t *Tools) mapNearby(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
	lng, err := paramFloat(params, "lng")
	if err != nil {
		return errorResult(err.Error()), nil
	}
	lat, err := paramFloat(params, "lat")
	if err != nil {
		return errorResult(err.Error()), nil
	}
	radius := paramFloatDefault(params, "radius_km", 10)
	category := paramStringDefault(params, "category", "")
	subcats := paramStringSlice(params, "subcategories")
	limit := paramIntDefault(params, "limit", 10)

	feats, dists, err := t.store.SearchMapNearby(ctx, lng, lat, radius, category, subcats, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("erreur de recherche proximité: %s", err)), nil
	}
	return jsonResult(map[string]any{"count": len(feats), "results": mapFeatureResults(feats, dists)}), nil
}

func (t *Tools) mapWithin(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
	zone, err := paramString(params, "zone")
	if err != nil {
		return errorResult(err.Error()), nil
	}
	category := paramStringDefault(params, "category", "")
	subcats := paramStringSlice(params, "subcategories")
	limit := paramIntDefault(params, "limit", 10)

	feats, err := t.store.SearchMapWithin(ctx, zone, category, subcats, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("erreur de recherche dans la zone: %s", err)), nil
	}
	return jsonResult(map[string]any{"count": len(feats), "zone": zone, "results": mapFeatureResults(feats, nil)}), nil
}

func (t *Tools) guideSearch(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
	query, err := paramString(params, "query")
	if err != nil {
		return errorResult(err.Error()), nil
	}
	limit := paramIntDefault(params, "limit", 5)

	if t.guide == nil || !t.guide.Ready() {
		return errorResult("guide non disponible"), nil
	}

	result, err := t.guide.Search(ctx, query, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("erreur de recherche guide: %s", err)), nil
	}
	return llm.NewToolResult(result), nil
}

func mapFeatureResults(feats []store.MapFeature, dists []float64) []map[string]any {
	results := make([]map[string]any, 0, len(feats))
	for i, f := range feats {
		item := map[string]any{
			"name":     f.Name,
			"category": f.CategoryCode,
		}
		if f.Title != "" {
			item["title"] = f.Title
		}
		if f.Address != "" {
			item["address"] = f.Address
		}
		if f.Phone != "" {
			item["phone"] = f.Phone
		}
		if len(f.Subcategories) > 0 {
			item["subcategories"] = f.Subcategories
		}
		if dists != nil && i < len(dists) {
			item["distance_km"] = fmt.Sprintf("%.1f", dists[i])
		}
		results = append(results, item)
	}
	return results
}

// --- helpers ---

func errorResult(message string) llm.ToolResult {
	payload, _ := json.Marshal(map[string]string{"error": message})
	return llm.NewToolResult(string(payload))
}

func jsonResult(v any) llm.ToolResult {
	payload, err := json.Marshal(v)
	if err != nil {
		return errorResult("erreur d'encodage")
	}
	return llm.NewToolResult(string(payload))
}

func paramString(params map[string]any, name string) (string, error) {
	raw, exists := params[name]
	if !exists {
		return "", fmt.Errorf("paramètre manquant: %s", name)
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	case float64:
		return fmt.Sprintf("%g", v), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func paramStringDefault(params map[string]any, name, def string) string {
	s, err := paramString(params, name)
	if err != nil || s == "" {
		return def
	}
	return s
}

func paramStringSlice(params map[string]any, name string) []string {
	raw, exists := params[name]
	if !exists {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func paramFloat(params map[string]any, name string) (float64, error) {
	raw, exists := params[name]
	if !exists {
		return 0, fmt.Errorf("paramètre manquant: %s", name)
	}
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err != nil {
			return 0, fmt.Errorf("paramètre invalide %s: %q", name, v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("paramètre invalide %s: %v", name, raw)
	}
}

func paramFloatDefault(params map[string]any, name string, def float64) float64 {
	f, err := paramFloat(params, name)
	if err != nil {
		return def
	}
	return f
}

func paramIntDefault(params map[string]any, name string, def int) int {
	raw, exists := params[name]
	if !exists {
		return def
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

var _ = time.Second // reserve
