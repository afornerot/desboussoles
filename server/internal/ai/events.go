package ai

import "encoding/json"

// StreamEvent est un événement émis pendant le streaming du chat.
type StreamEvent struct {
	Type  string   `json:"type"` // token | tool | establishments | done | error
	Token string   `json:"t,omitempty"`
	Tool  string   `json:"name,omitempty"`
	Tools []string `json:"tools_used,omitempty"`
	Error string   `json:"error,omitempty"`
	// Establishments porte les résultats d'un outil carte (map_search,
	// map_nearby, map_within) pour les afficher dans la colonne dédiée.
	Establishments []Establishment `json:"establishments,omitempty"`
}

// Establishment est un établissement renvoyé par un outil carte.
type Establishment struct {
	ID            int      `json:"id,omitempty"`
	Name          string   `json:"name"`
	Title         string   `json:"title,omitempty"`
	Address       string   `json:"address,omitempty"`
	Phone         string   `json:"phone,omitempty"`
	Category      string   `json:"category,omitempty"`
	Subcategories []string `json:"subcategories,omitempty"`
	DistanceKm    string   `json:"distance_km,omitempty"`
	Lng           float64  `json:"lng,omitempty"`
	Lat           float64  `json:"lat,omitempty"`
}

// HistoryMessage est un message d'historique (session).
type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest est le corps de POST /api/chat.
type ChatRequest struct {
	Message string `json:"message"`
	// GuideContext est le contexte du questionnaire « Guide » (réponses +
	// dernière réponse API, JSON). Injecté dans le prompt du chat public.
	GuideContext json.RawMessage `json:"guide_context,omitempty"`
}
