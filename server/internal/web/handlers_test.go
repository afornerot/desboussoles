package web

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGuideLocation(t *testing.T) {
	marshal := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	base := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "questionnaire seul",
			raw:  marshal(map[string]any{"q_commune": "Ornans", "q_commune_at": at(0)}),
			want: "Ornans",
		},
		{
			name: "chat plus récent que le questionnaire",
			raw: marshal(map[string]any{
				"q_commune": "Ornans", "q_commune_at": at(0),
				"chat_loc": "Besançon", "chat_loc_at": at(5),
			}),
			want: "Besançon",
		},
		{
			name: "questionnaire changé après le chat — ton cas",
			raw: marshal(map[string]any{
				"chat_loc": "Besançon", "chat_loc_at": at(5),
				"q_commune": "Dijon", "q_commune_at": at(9),
			}),
			want: "Dijon",
		},
		{
			name: "questionnaire inchangé : le choix du chat reste",
			raw: marshal(map[string]any{
				"q_commune": "Ornans", "q_commune_at": at(0),
				"chat_loc": "Besançon", "chat_loc_at": at(5),
			}),
			want: "Besançon",
		},
		{
			name: "vide",
			raw:  marshal(map[string]any{}),
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := guideLocation(c.raw); got != c.want {
				t.Fatalf("guideLocation = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGuideCommuneShapes(t *testing.T) {
	// Forme enveloppée (contexte de session).
	wrapped := []byte(`{"json_patient":{"personne":{"commune_nom":{"value":"Ornans"}}}}`)
	if got := guideCommune(wrapped); got != "Ornans" {
		t.Fatalf("enveloppée = %q", got)
	}
	// Forme nue (json_patient direct).
	bare := []byte(`{"personne":{"commune_nom":{"value":"Besançon"}}}`)
	if got := guideCommune(bare); got != "Besançon" {
		t.Fatalf("nue = %q", got)
	}
}

func TestGuideAge(t *testing.T) {
	marshal := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	base := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

	// L'âge dit au chat prime s'il est plus récent.
	got := guideAge(marshal(map[string]any{
		"q_age": "15", "q_age_at": at(0),
		"chat_age": "25", "chat_age_at": at(5),
	}))
	if got != "25" {
		t.Fatalf("chat plus récent = %q", got)
	}
	// Le questionnaire re-changé après le chat reprend la main.
	got = guideAge(marshal(map[string]any{
		"chat_age": "25", "chat_age_at": at(5),
		"q_age": "30", "q_age_at": at(9),
	}))
	if got != "30" {
		t.Fatalf("questionnaire plus récent = %q", got)
	}
	// Questionnaire inchangé : le choix du chat reste.
	got = guideAge(marshal(map[string]any{
		"q_age": "15", "q_age_at": at(0),
		"chat_age": "25", "chat_age_at": at(5),
	}))
	if got != "25" {
		t.Fatalf("valeur conservée = %q", got)
	}
}

func TestGuideAgeValue(t *testing.T) {
	if got := guideAgeValue([]byte(`{"personne":{"age_ans":{"value":15}}}`)); got != "15" {
		t.Fatalf("âge nu = %q", got)
	}
	if got := guideAgeValue([]byte(`{"json_patient":{"personne":{"age_ans":{"value":42}}}}`)); got != "42" {
		t.Fatalf("âge enveloppé = %q", got)
	}
	if got := guideAgeValue([]byte(`{"personne":{"age_ans":{"value":"pass"}}}`)); got != "" {
		t.Fatalf("pass = %q", got)
	}
}

func TestGuideAddress(t *testing.T) {
	marshal := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	base := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

	// L'adresse dite au chat prime si elle est plus récente.
	got := guideAddress(marshal(map[string]any{
		"q_addr": "12 rue Victor Hugo, 25000 Besançon", "q_addr_at": at(0),
		"chat_addr": "5 place de la République, 25500 Ornans", "chat_addr_at": at(5),
	}))
	if got != "5 place de la République, 25500 Ornans" {
		t.Fatalf("chat plus récent = %q", got)
	}
	// Le questionnaire re-changé après le chat reprend la main (q_adresse).
	got = guideAddress(marshal(map[string]any{
		"chat_addr": "5 place de la République, 25500 Ornans", "chat_addr_at": at(5),
		"q_addr": "1 rue de la Paix, 21000 Dijon", "q_addr_at": at(9),
	}))
	if got != "1 rue de la Paix, 21000 Dijon" {
		t.Fatalf("questionnaire plus récent = %q", got)
	}
}
