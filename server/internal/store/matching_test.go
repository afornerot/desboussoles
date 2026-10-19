package store

import (
	"testing"
)

func TestSignificantWords(t *testing.T) {
	got := significantWords("Hôpital de jour « Nov'ado » – Centre Hospitalier de Novillars (CHN)")
	want := map[string]bool{
		"hopital": true, "jour": true, "nov": true, "ado": true,
		"centre": true, "hospitalier": true, "novillars": true, "chn": true,
	}
	if len(got) != len(want) {
		t.Fatalf("mots = %v", got)
	}
	for _, w := range got {
		if !want[w] {
			t.Errorf("mot inattendu %q", w)
		}
	}
	// « de » est trop court, « les » est un mot-outil.
	for _, w := range significantWords("CMPP de Besançon les enfants") {
		if w == "de" || w == "les" {
			t.Errorf("mot-outil conservé : %q", w)
		}
	}
}

func TestScoreFeature(t *testing.T) {
	words := significantWords("Hôpital de jour « Nov'ado » – Centre Hospitalier de Novillars (CHN)")
	target := "Hôpital de Jour Adolescents : Nov'Ado Chn"

	// Le nom enrichi du LLM doit scorer nettement au-dessus d'un établissement
	// sans rapport, malgré les différences d'intitulé et d'accents.
	score := scoreFeature(target, "", "4 Rue Louis Garnier, 25000 Besançon", "Hôpital de jour « Nov'ado » – Centre Hospitalier de Novillars (CHN)", words)
	other := scoreFeature("CMPP Morteau Ahs Fc", "", "8c Avenue Charles de Gaulle, 25500 Morteau", "Hôpital de jour « Nov'ado » – Centre Hospitalier de Novillars (CHN)", words)
	if score <= other {
		t.Fatalf("score cible %d <= score autre %d", score, other)
	}
	if score < 30 {
		t.Fatalf("score cible trop faible : %d", score)
	}

	// L'adresse compte : « CMPP de Besançon » distingue le CMPP dont
	// l'adresse est à Besançon.
	words2 := significantWords("CMPP de Besançon")
	besancon := scoreFeature("CMPP Avenue Denfert-Rochereau Ahs Fc", "", "15 Avenue Denfert-Rochereau, 25000 Besançon", "CMPP de Besançon", words2)
	pontarlier := scoreFeature("CMPP Pontarlier Ahs Fc", "", "1 Rue des Abbés Cattet, 25300 Pontarlier", "CMPP de Besançon", words2)
	if besancon <= pontarlier {
		t.Fatalf("Besançon %d <= Pontarlier %d (l'adresse doit discriminer)", besancon, pontarlier)
	}

	// Insensible aux accents : « Medico-Psychologique » retrouve « Médico-Psychologique ».
	words3 := significantWords("centre medico-psychologique")
	if scoreFeature("Centre Médico-Psychologique", "", "", "centre medico-psychologique", words3) == 0 {
		t.Fatal("accents non normalisés")
	}
}

func TestZoneBonus(t *testing.T) {
	// Une zone à Besançon (centre 6.02 / 47.25).
	zone := &UserZone{
		Name:   "Besançon",
		In:     map[int]bool{42: true},
		Center: [2]float64{6.02, 47.25},
	}
	in := MapFeature{ID: 42, Name: "X", Lng: 6.05, Lat: 47.24}
	near := MapFeature{ID: 1, Name: "Y", Lng: 6.15, Lat: 47.10}  // Ornans ~17 km
	far := MapFeature{ID: 2, Name: "Z", Lng: 6.36, Lat: 46.90}   // Pontarlier ~45 km

	if got := zoneBonus(in, zone); got != 100 {
		t.Fatalf("contenu dans la zone = %d, want 100", got)
	}
	if got := zoneBonus(near, zone); got != 20 {
		t.Fatalf("à ~17 km = %d, want 20", got)
	}
	if got := zoneBonus(far, zone); got != 10 {
		t.Fatalf("à ~45 km = %d, want 10", got)
	}
	if got := zoneBonus(far, nil); got != 0 {
		t.Fatalf("sans zone = %d, want 0", got)
	}
}

func TestRankFeatures(t *testing.T) {
	items := []MapFeature{
		{Name: "CMPP Morteau Ahs Fc"},
		{Name: "Hôpital de Jour Adolescents : Nov'Ado Chn"},
		{Name: "CMPP Pontarlier Ahs Fc"},
	}
	got := rankFeatures("Nov'ado", items, 2, nil)
	if len(got) != 2 {
		t.Fatalf("taille = %d", len(got))
	}
	if got[0].Name != "Hôpital de Jour Adolescents : Nov'Ado Chn" {
		t.Fatalf("tri = %v", got)
	}

	// Départage par distance : à score égal, le plus proche passe devant —
	// y compris pour un usager isolé (bonus 0 partout).
	zone := &UserZone{Name: "Ornans", In: map[int]bool{}, Center: [2]float64{6.15, 47.10}}
	same := []MapFeature{
		{ID: 10, Name: "Structure A", Lng: 6.36, Lat: 46.90}, // ~45 km
		{ID: 11, Name: "Structure B", Lng: 6.20, Lat: 47.05}, // ~7 km
	}
	got2 := rankFeatures("zzz inconnu", same, 2, zone)
	if got2[0].Name != "Structure B" {
		t.Fatalf("départage distance = %v", got2)
	}
}
