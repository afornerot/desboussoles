package ai

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/bornholm/genai/llm"
)

// failingClient simule un provider LLM en échec (quota épuisé, panne réseau).
type failingClient struct{}

func (failingClient) ChatCompletion(context.Context, ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	return nil, errors.New("panne")
}

func (failingClient) ChatCompletionStream(context.Context, ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("http 429: quota épuisé")
}

func (failingClient) Embeddings(context.Context, []string, ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	return nil, errors.New("panne")
}

func (failingClient) Transcription(context.Context, []byte, ...llm.TranscriptionOptionFunc) (llm.TranscriptionResponse, error) {
	return nil, errors.New("panne")
}

// TestChatAlwaysAnswers vérifie la règle d'or : même en échec du provider,
// l'utilisateur reçoit le message de repli (orientation), jamais une erreur
// technique.
func TestChatAlwaysAnswers(t *testing.T) {
	svc := New(failingClient{}, nil, Config{
		MaxIterations: 2,
		CallTimeout:   5 * time.Second,
		Budget:        NewBudget(Limits{MaxRequestTokens: 8000, RequestsPerDay: 1000}),
	}, slog.Default())

	var tokens string
	sawDone, sawError := false, false
	for ev := range svc.Chat(context.Background(), nil, "Bonjour", SystemPromptPro) {
		switch ev.Type {
		case "token":
			tokens += ev.Token
		case "done":
			sawDone = true
		case "error":
			sawError = true
		}
	}
	if sawError {
		t.Fatal("événement error émis : le fallback doit être envoyé comme réponse")
	}
	if !sawDone {
		t.Fatal("événement done manquant")
	}
	if tokens != FallbackMessage {
		t.Fatalf("réponse = %q, want FallbackMessage", tokens)
	}
}

func TestDecodeStructures(t *testing.T) {
	got := decodeStructures("Voici : ```json\n{\"structures\":[{\"nom\":\"CMPP de Besançon\",\"ville\":\"Besançon\"},{\"nom\":\"EMAHD\",\"ville\":\"\"}],\"localisation\":\"Pontarlier\"}\n```")
	if len(got.Structures) != 2 || got.Structures[0].Nom != "CMPP de Besançon" || got.Structures[0].Ville != "Besançon" {
		t.Fatalf("structures = %+v", got)
	}
	if got.Localisation != "Pontarlier" {
		t.Fatalf("localisation = %q", got.Localisation)
	}
	// Ancien format {"names": [...]} toléré.
	if got := decodeStructures(`{"names": ["Nov'ado"]}`); len(got.Structures) != 1 || got.Structures[0].Nom != "Nov'ado" {
		t.Fatalf("ancien format = %+v", got)
	}
}

func TestPickRecommended(t *testing.T) {
	bon := Establishment{
		Name:    "CMPP Avenue Denfert-Rochereau Ahs Fc",
		Address: "15 Avenue Denfert-Rochereau, 25000 Besançon",
	}
	lointain := Establishment{
		Name:    "Maison de Santé Avec Psychologue (article 51)",
		Address: "17 Grande Rue, 25520 Goux-les-Usiers",
	}
	ville := Establishment{Name: "Ornans", Address: ""}

	// Bonne correspondance : nom + ville cohérente.
	if _, ok := pickRecommended(recommendedStructure{Nom: "CMPP de Besançon", Ville: "Besançon"}, []Establishment{bon}); !ok {
		t.Fatal("CMPP de Besançon devrait matcher le CMPP de Besançon")
	}
	// Nom proche mais ville incohérente : refusé (rien plutôt qu'un faux).
	if _, ok := pickRecommended(recommendedStructure{Nom: "SASS – Maison de l'Enfance", Ville: "Besançon"}, []Establishment{lointain}); ok {
		t.Fatal("une correspondance hors ville ne doit pas être affichée")
	}
	// Une ville n'est jamais un établissement.
	if _, ok := pickRecommended(recommendedStructure{Nom: "Maison de Santé du Canton d'Ornans", Ville: "Ornans"}, []Establishment{ville}); ok {
		t.Fatal("une ville ne doit pas être affichée comme établissement")
	}
	// Trop faible en correspondance de nom : refusé même sans ville annoncée.
	if _, ok := pickRecommended(recommendedStructure{Nom: "Équipe Mobile Adolescents Haut-Doubs", Ville: ""}, []Establishment{lointain}); ok {
		t.Fatal("correspondance faible : ne rien afficher")
	}
	// Faute de frappe dans la ville annoncée : tolérée (« Poutarlier »).
	pontarlier := Establishment{
		Name:    "CMPP Pontarlier Ahs Fc",
		Address: "1 Rue des Abbés Cattet, 25300 Pontarlier",
	}
	if _, ok := pickRecommended(recommendedStructure{Nom: "CMPP de Poutarlier", Ville: "Poutarlier"}, []Establishment{pontarlier}); !ok {
		t.Fatal("une faute de frappe de ville ne doit pas faire disparaître la structure")
	}
}

func TestPickRecommendedRealCases(t *testing.T) {
	// Cas observés en test réel (guide vs carte : intitulés différents).
	cattgPontarlier := Establishment{
		Name:    "Centre d'activités Thérapeutiques et de Temps de Groupe (cattg) de Pontarlier — Filière Adolescent",
		Address: "17 rue de Morteau, 25300 Pontarlier",
		Phone:   "03 81 38 45 67",
	}
	cmppMorteau := Establishment{
		Name:    "CMPP Morteau Ahs Fc",
		Address: "8c Avenue Charles de Gaulle, 25500 Morteau",
		Phone:   "03 81 67 97 00",
	}
	cmpCattp := Establishment{
		Name:    "C.m.p - C.a.t.t.p. de Pontarlier",
		Address: "2 Faubourg Saint-Etienne - 25300 Pontarlier",
		Phone:   "03 81 38 58 78",
	}
	cmppPontarlier := Establishment{
		Name:    "CMPP Pontarlier Ahs Fc",
		Address: "1 rue des Abbès Cattet, 25300 Pontarlier",
		Phone:   "03 81 38 88 44",
	}
	emtaa := Establishment{
		Name:    "Emtaa Équipe Mobile de Transition Vers L'âge Adulte",
		Address: "5 boulevard Diderot - 25000 Besançon",
		Phone:   "03 81 25 05 31",
	}
	emaHD := Establishment{
		Name:    "Équipe Mobile Adolescents Haut-Doubs (emahd)",
		Address: "17 rue de Morteau 25300 Pontarlier",
		Phone:   "03 81 25 05 32",
	}

	// « CMP de Morteau » : la rue de Morteau (Pontarlier) ne doit pas leurrer.
	got, ok := pickRecommended(recommendedStructure{Nom: "Centre Médico-Psychologique (CMP) de Morteau", Ville: "Morteau"},
		[]Establishment{cattgPontarlier, cmppMorteau})
	if !ok || got.Name != "CMPP Morteau Ahs Fc" {
		t.Fatalf("CMP de Morteau → %q (ok=%v)", got.Name, ok)
	}

	// « CMP de Pontarlier » avec téléphone : le numéro tranche entre CMP et CMPP.
	got, ok = pickRecommended(recommendedStructure{Nom: "Centre Médico-Psychologique (CMP) de Pontarlier", Ville: "Pontarlier", Telephone: "03 81 38 58 78"},
		[]Establishment{cmppPontarlier, cmpCattp})
	if !ok || got.Name != "C.m.p - C.a.t.t.p. de Pontarlier" {
		t.Fatalf("CMP de Pontarlier (tél) → %q (ok=%v)", got.Name, ok)
	}

	// Sans téléphone, le nom avec ponctuation doit être reconnu (« C.m.p »).
	got, ok = pickRecommended(recommendedStructure{Nom: "C.m.p de Pontarlier", Ville: "Pontarlier"},
		[]Establishment{cmppPontarlier, cmpCattp})
	if !ok || got.Name != "C.m.p - C.a.t.t.p. de Pontarlier" {
		t.Fatalf("C.m.p (ponctuation) → %q (ok=%v)", got.Name, ok)
	}

	// EMAHD : le vrai l'emporte sur EMTAA (mots partagés génériques).
	got, ok = pickRecommended(recommendedStructure{Nom: "Équipe Mobile Adolescents Haut-Doubs (EMAHD)", Ville: ""},
		[]Establishment{emtaa, emaHD})
	if !ok || got.Name != "Équipe Mobile Adolescents Haut-Doubs (emahd)" {
		t.Fatalf("EMAHD → %q (ok=%v)", got.Name, ok)
	}

	// EMAHD absent de la base, avec téléphone : rien plutôt qu'EMTAA.
	if _, ok := pickRecommended(recommendedStructure{Nom: "Équipe Mobile Adolescents Haut-Doubs (EMAHD)", Telephone: "03 99 99 99 99"},
		[]Establishment{emtaa}); ok {
		t.Fatal("un faux jumeau ne doit pas s'afficher quand le téléphone ne correspond pas")
	}
}

func TestPickRecommendedNamingDivergence(t *testing.T) {
	// Le guide et la carte nomment différemment les mêmes structures (et les
	// numéros divergent) : la correspondance doit passer quand même.
	mda := Establishment{
		Name:    "Maison des Adolescents du Doubs (mda-25) Chn",
		Address: "15 Rue du Bouchot, 25000 Besançon",
		Phone:   "03 81 53 97 67",
	}
	chuPsy := Establishment{
		Name:    "Service de Consultation Hospitalo-Universitaire de Psychiatrie de L'enfant et de L'adolescent",
		Address: "2 Avenue de la Santé, 25000 Besançon",
		Phone:   "03 81 21 81 54",
	}

	got, ok := pickRecommended(recommendedStructure{
		Nom: "Maison des Adolescents (MDA) – Besançon", Ville: "Besançon", Telephone: "03 81 88 95 55",
	}, []Establishment{mda, chuPsy})
	if !ok || got.Name != mda.Name {
		t.Fatalf("MDA → %q (ok=%v)", got.Name, ok)
	}

	got, ok = pickRecommended(recommendedStructure{
		Nom: "Service de Psychiatrie Infanto-Juvenile du CHU de Besançon", Ville: "Besançon", Telephone: "03 81 88 30 00",
	}, []Establishment{chuPsy, mda})
	if !ok || got.Name != chuPsy.Name {
		t.Fatalf("CHU psychiatrie → %q (ok=%v)", got.Name, ok)
	}
}

func TestPickRecommendedCGIBesancon(t *testing.T) {
	// Le guide dit « Centre de Gestion Infantile (CGI) », la carte
	// « Centre de Guidance Infanto-Juvénile » — même service, intitulés divergents.
	jardins := Establishment{
		Name:    "Centre de Guidance Infanto Juvénile : Rue des Jardins Chn",
		Address: "13 Rue des Jardins - 25000 Besançon",
		Phone:   "03 81 53 48 02",
	}
	got, ok := pickRecommended(recommendedStructure{
		Nom: "Centre de Gestion Infantile (CGI) – Besançon", Ville: "Besançon",
	}, []Establishment{jardins})
	if !ok || got.Name != jardins.Name {
		t.Fatalf("CGI Besançon → %q (ok=%v)", got.Name, ok)
	}
}
