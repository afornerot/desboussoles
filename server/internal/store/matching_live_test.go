package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSearchMapFeaturesLive confronte la correspondance aux vrais noms de la
// base avec des formulations « de LLM » (enrichies, accentuées autrement) et
// vérifie le ciblage par zone utilisateur.
// Exécution : TEST_DATABASE_URL=postgres://… go test -run Live ./internal/store/
func TestSearchMapFeaturesLive(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL absente — test d'intégration ignoré")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Correspondance par nom, sans zone.
	nomCases := []struct {
		query string
		want  string
	}{
		{"Hôpital de jour « Nov'ado » – Centre Hospitalier de Novillars (CHN)", "nov'ado"},
		{"centre medico-psychologique morteau", "morteau"},
	}
	for _, c := range nomCases {
		t.Run("nom/"+c.query, func(t *testing.T) {
			feats, err := st.SearchMapFeatures(ctx, c.query, "etablissement", nil, 5, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(feats) == 0 {
				t.Fatalf("aucun résultat pour %q", c.query)
			}
			if !strings.Contains(NormalizeName(feats[0].Name), c.want) {
				t.Fatalf("1er résultat = %q, want contenant %q (tous : %v)",
					feats[0].Name, c.want, names(feats))
			}
		})
	}

	// Ciblage par zone : « CMPP » doit donner le CMPP de la zone de l'utilisateur.
	zoneCases := []struct {
		zone     string
		wantAddr string
	}{
		{"Besançon", "besancon"},
		{"Pontarlier", "pontarlier"},
	}
	for _, c := range zoneCases {
		t.Run("zone/"+c.zone, func(t *testing.T) {
			z := st.UserZoneInfo(ctx, c.zone)
			if z == nil {
				t.Fatalf("zone %q introuvable", c.zone)
			}
			feats, err := st.SearchMapFeatures(ctx, "CMPP", "etablissement", nil, 5, z)
			if err != nil {
				t.Fatal(err)
			}
			if len(feats) == 0 {
				t.Fatal("aucun CMPP trouvé")
			}
			if !strings.Contains(NormalizeName(feats[0].Address), c.wantAddr) {
				t.Fatalf("1er CMPP = %q (%q), want adresse contenant %q (tous : %v)",
					feats[0].Name, feats[0].Address, c.wantAddr, names(feats))
			}
		})
	}

	// Usager isolé (Ornans) : rien ne doit disparaître, le plus proche départage.
	t.Run("zone/Ornans éloigné", func(t *testing.T) {
		z := st.UserZoneInfo(ctx, "Ornans")
		if z == nil {
			t.Fatal("zone Ornans introuvable")
		}
		feats, err := st.SearchMapFeatures(ctx, "Hôpital de jour Nov'ado", "etablissement", nil, 5, z)
		if err != nil {
			t.Fatal(err)
		}
		if len(feats) == 0 {
			t.Fatal("Nov'ado a disparu pour un usager d'Ornans (la zone est un bonus, pas un filtre)")
		}
		if !strings.Contains(NormalizeName(feats[0].Name), "nov'ado") {
			t.Fatalf("1er résultat = %q, want Nov'ado", feats[0].Name)
		}
	})
}

func names(feats []MapFeature) []string {
	out := make([]string, len(feats))
	for i, f := range feats {
		out[i] = f.Name
	}
	return out
}
