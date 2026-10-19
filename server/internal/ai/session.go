package ai

import (
	"fmt"
	"sync"
	"time"
)

// SessionStore maintient l'historique de conversation par session (en mémoire, TTL).
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session
	ttl      time.Duration
	maxChars int
}

type session struct {
	messages []HistoryMessage
	ests     []Establishment
	updated  time.Time
}

// NewSessionStore crée un store avec TTL (défaut 1h) et un budget d'historique
// en caractères (défaut maxHistoryChars), issu des limites du provider.
func NewSessionStore(ttl time.Duration, maxChars int) *SessionStore {
	if ttl <= 0 {
		ttl = time.Hour
	}
	if maxChars <= 0 {
		maxChars = maxHistoryChars
	}
	ss := &SessionStore{
		sessions: make(map[string]*session),
		ttl:      ttl,
		maxChars: maxChars,
	}
	go ss.cleanupLoop()
	return ss
}

func (ss *SessionStore) cleanupLoop() {
	t := time.NewTicker(10 * time.Minute)
	for range t.C {
		ss.cleanup()
	}
}

func (ss *SessionStore) cleanup() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := time.Now()
	for id, s := range ss.sessions {
		if now.Sub(s.updated) > ss.ttl {
			delete(ss.sessions, id)
		}
	}
}

// Add ajoute un échange user/assistant à la session.
func (ss *SessionStore) Add(sid, userMsg, assistantMsg string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		s = &session{}
		ss.sessions[sid] = s
	}
	s.messages = append(s.messages,
		HistoryMessage{Role: "user", Content: userMsg},
		HistoryMessage{Role: "assistant", Content: assistantMsg},
	)
	// Bornage par la taille : garder au moins le dernier échange, retirer les
	// plus anciens tant que l'historique dépasse le budget de tokens.
	for len(s.messages) > 2 && historyChars(s.messages) > ss.maxChars {
		s.messages = s.messages[1:]
	}
	s.updated = time.Now()
}

// historyChars mesure la taille approximative d'un historique.
func historyChars(msgs []HistoryMessage) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
	}
	return n
}

// History retourne l'historique de la session.
func (ss *SessionStore) History(sid string) []HistoryMessage {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		return nil
	}
	out := make([]HistoryMessage, len(s.messages))
	copy(out, s.messages)
	return out
}

// estabKey identifie un établissement pour le dédoublonnage.
func estabKey(e Establishment) string {
	if e.ID != 0 {
		return fmt.Sprintf("id:%d", e.ID)
	}
	return "name:" + e.Name + "|" + e.Address
}

// AddEstablishments cumule les établissements d'un outil carte dans la session
// (dédoublonnés), afin de les retrouver après navigation vers une autre page.
func (ss *SessionStore) AddEstablishments(sid string, ests []Establishment) {
	if len(ests) == 0 {
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		s = &session{}
		ss.sessions[sid] = s
	}
	seen := make(map[string]bool, len(s.ests))
	for _, e := range s.ests {
		seen[estabKey(e)] = true
	}
	for _, e := range ests {
		k := estabKey(e)
		if seen[k] {
			continue
		}
		seen[k] = true
		s.ests = append(s.ests, e)
	}
	if len(s.ests) > 100 {
		s.ests = s.ests[len(s.ests)-100:]
	}
	s.updated = time.Now()
}

// Establishments retourne les établissements accumulés pour la session.
func (ss *SessionStore) Establishments(sid string) []Establishment {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		return nil
	}
	out := make([]Establishment, len(s.ests))
	copy(out, s.ests)
	return out
}

// ClearEstablishments vide la liste d'établissements sans toucher à l'historique.
func (ss *SessionStore) ClearEstablishments(sid string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if s, ok := ss.sessions[sid]; ok {
		s.ests = nil
		s.updated = time.Now()
	}
}

// Reset efface l'historique d'une session.
func (ss *SessionStore) Reset(sid string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.sessions, sid)
}
