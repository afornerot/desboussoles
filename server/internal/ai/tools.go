package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bornholm/genai/llm"

	"boussole/internal/store"
)

// Tools expose les outils LLM de Boussole (lexique + carte PostGIS + guide).
type Tools struct {
	store  *store.Store
	guide  *Guide
	budget *Budget
}

// NewTools construit l'ensemble des outils. Le budget borne la taille des
// résultats renvoyés au modèle.
func NewTools(st *store.Store, guide *Guide, budget *Budget) *Tools {
	return &Tools{store: st, guide: guide, budget: budget}
}

// toolResultLimit retourne la taille maximale d'un résultat d'outil.
func (t *Tools) toolResultLimit() int {
	if t.budget != nil && t.budget.MaxToolResultChars > 0 {
		return t.budget.MaxToolResultChars
	}
	return maxToolResultChars
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
		llm.NewFuncTool(
			"geocode",
			"Convertit une adresse française (ou une partie d'adresse) en coordonnées GPS (longitude, latitude). Utilise l'adresse présente dans le contexte du guide ou de la conversation pour pouvoir ensuite appeler map_nearby.",
			llm.NewJSONSchema().
				RequiredProperty("address", "Adresse à géocoder (ex: \"12 rue des Lilas, Besançon\" ou \"Besançon\")", "string"),
			t.geocode,
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

	feats, err := t.store.SearchMapFeatures(ctx, query, category, subcats, limit, store.ZoneFrom(ctx))
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

	// Aucun résultat dans le rayon demandé : on élargit progressivement.
	used := radius
	for i := 0; len(feats) == 0 && i < 3; i++ {
		used = radius * []float64{3, 6, 12}[i]
		if used > 80 {
			used = 80
		}
		feats, dists, err = t.store.SearchMapNearby(ctx, lng, lat, used, category, subcats, limit)
		if err != nil {
			return errorResult(fmt.Sprintf("erreur de recherche proximité: %s", err)), nil
		}
	}

	out := map[string]any{"count": len(feats), "results": mapFeatureResults(feats, dists)}
	if used != radius {
		out["rayon_utilise_km"] = used
		if len(feats) == 0 {
			out["note"] = fmt.Sprintf("aucun résultat trouvé même dans un rayon de %.0f km", used)
		} else {
			out["note"] = fmt.Sprintf("aucun résultat dans %.0f km, recherche élargie à %.0f km — précise que les résultats sont les plus proches", radius, used)
		}
	}
	return jsonResult(out), nil
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

	result, err := t.guide.Search(ctx, query, limit, t.toolResultLimit())
	if err != nil {
		return errorResult(fmt.Sprintf("erreur de recherche guide: %s", err)), nil
	}
	return llm.NewToolResult(result), nil
}

// geocode convertit une adresse en coordonnées GPS via l'API BAN
// (Base Adresse Nationale, api-adresse.data.gouv.fr — gratuite, sans clé).
// GeoResult porte la réponse de l'API BAN (Base Adresse Nationale).
type GeoResult struct {
	Label       string
	Name        string
	City        string
	Postcode    string
	Citycode    string
	Context     string
	Score       float64
	Lng, Lat    float64
	Housenumber string
	Street      string
}

// GeocodeAddress interroge l'API BAN (api-adresse.data.gouv.fr — gratuite,
// sans clé) et retourne la meilleure correspondance pour une adresse ou une
// commune. Utilisé par l'outil geocode et pour localiser la commune du
// questionnaire absente de la carte.
func GeocodeAddress(ctx context.Context, address string) (*GeoResult, bool) {
	reqURL := "https://api-adresse.data.gouv.fr/search?q=" + url.QueryEscape(address) + "&limit=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, false
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, false
	}
	var geoResp struct {
		Features []struct {
			Geometry struct {
				Coordinates [2]float64 `json:"coordinates"` // [lng, lat]
			} `json:"geometry"`
			Properties struct {
				Label       string  `json:"label"`
				Name        string  `json:"name"`
				City        string  `json:"city"`
				Postcode    string  `json:"postcode"`
				Citycode    string  `json:"citycode"`
				Score       float64 `json:"score"`
				Type        string  `json:"type"`
				Context     string  `json:"context"`
				Housenumber string  `json:"housenumber"`
				Street      string  `json:"street"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &geoResp); err != nil || len(geoResp.Features) == 0 {
		return nil, false
	}
	f := geoResp.Features[0]
	return &GeoResult{
		Label: f.Properties.Label, Name: f.Properties.Name,
		City: f.Properties.City, Postcode: f.Properties.Postcode,
		Citycode: f.Properties.Citycode, Context: f.Properties.Context,
		Score: f.Properties.Score, Lng: f.Geometry.Coordinates[0], Lat: f.Geometry.Coordinates[1],
		Housenumber: f.Properties.Housenumber, Street: f.Properties.Street,
	}, true
}

// geocode convertit une adresse en coordonnées GPS via l'API BAN.
func (t *Tools) geocode(ctx context.Context, params map[string]any) (llm.ToolResult, error) {
	address, err := paramString(params, "address")
	if err != nil {
		return errorResult(err.Error()), nil
	}
	g, ok := GeocodeAddress(ctx, address)
	if !ok {
		return jsonResult(map[string]any{
			"found":   false,
			"address": address,
			"note":    "aucune correspondance trouvée pour cette adresse",
		}), nil
	}
	return jsonResult(map[string]any{
		"found":    true,
		"label":    g.Label,
		"lng":      g.Lng,
		"lat":      g.Lat,
		"city":     g.City,
		"postcode": g.Postcode,
		"citycode": g.Citycode,
		"context":  g.Context,
		"score":    g.Score,
	}), nil
}

func mapFeatureResults(feats []store.MapFeature, dists []float64) []map[string]any {
	results := make([]map[string]any, 0, len(feats))
	for i, f := range feats {
		item := map[string]any{
			"id":       f.ID,
			"name":     f.Name,
			"category": f.CategoryCode,
			"lng":      f.Lng,
			"lat":      f.Lat,
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
