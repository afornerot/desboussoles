package store

import (
	"math"
	"sort"
	"strings"
)

// matching.go — correspondance souple entre les noms produits par le LLM et
// les noms d'établissements en base : insensible aux accents et à la casse,
// bidirectionnelle (chaîne dans nom, ou nom dans chaîne), et par recouvrement
// de mots significatifs. Un intitulé « enrichi » par le modèle (« Hôpital de
// jour Nov'ado – CHN ») doit retrouver « Hôpital de Jour Adolescents :
// Nov'Ado Chn ».

// stopwords retirées du découpage en mots (trop fréquentes pour discriminer).
var matchStopwords = map[string]bool{
	"avec": true, "dans": true, "pour": true, "sans": true, "sur": true,
	"vers": true, "entre": true, "depuis": true, "lors": true, "quand": true,
	"peut": true, "afin": true, "ainsi": true, "donc": true, "mais": true,
	"plus": true, "moins": true, "tout": true, "tous": true, "toute": true,
	"leur": true, "leurs": true, "notre": true, "votre": true, "cette": true,
	"comme": true, "etre": true, "avoir": true, "fait": true,
	"les": true, "des": true, "une": true, "aux": true, "par": true,
	"ses": true, "son": true, "nos": true, "vos": true, "est": true,
	"ont": true, "deux": true, "autre": true, "autres": true, "meme": true,
	"tres": true, "bien": true, "avant": true, "apres": true,
}

// stripAccents remplace les diacritiques français par leur lettre de base.
func stripAccents(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'à', 'â', 'ä', 'á', 'ã', 'å':
			return 'a'
		case 'é', 'è', 'ê', 'ë', 'ẽ':
			return 'e'
		case 'î', 'ï', 'í', 'ì', 'ĩ':
			return 'i'
		case 'ô', 'ö', 'ó', 'ò', 'õ':
			return 'o'
		case 'ù', 'û', 'ü', 'ú', 'ũ':
			return 'u'
		case 'ç', 'ć', 'č':
			return 'c'
		case 'ñ':
			return 'n'
		case 'ÿ', 'ý':
			return 'y'
		case 'œ':
			return 'e'
		case 'æ':
			return 'a'
		}
		return r
	}, s)
}

// normalizeName produit une forme comparable : minuscules, sans accents.
func normalizeName(s string) string {
	return strings.ToLower(stripAccents(strings.TrimSpace(s)))
}

// significantWords découpe une requête en mots comparables (normalisés,
// ≥ 3 caractères — les sigles CMP, CHN, EHPAD comptent —, hors mots-outils).
// Les apostrophes servent de séparateurs (« Nov'ado » → nov, ado).
func significantWords(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !('a' <= r && r <= 'z' || 'à' <= r && r <= 'ÿ')
	})
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, f := range fields {
		w := normalizeName(f)
		if len(w) < 3 || matchStopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// scoreFeature note la proximité entre la requête et un établissement.
// Les valeurs sont additives : sous-chaîne exacte > inclusion inverse > mots.
// L'adresse compte aussi : « CMPP de Besançon » doit distinguer le CMPP dont
// l'adresse est à Besançon.
func scoreFeature(name, title, address, query string, words []string) int {
	hay := normalizeName(name + " " + title + " " + address)
	q := normalizeName(query)
	score := 0
	if q != "" && strings.Contains(hay, q) {
		score += 100
	}
	if q != "" && len(q) >= 4 && strings.Contains(q, hay) {
		score += 50
	}
	for _, w := range words {
		if strings.Contains(hay, w) {
			score += 10
		}
	}
	return score
}

// zoneBonus privilégie les établissements de la zone de l'utilisateur :
// +100 s'ils sont CONTENUS dans le polygone de sa commune (les villes sont des
// zones, pas des points), sinon un bonus décroissant selon la distance.
// C'est un bonus, jamais un filtre : une structure pertinente peut se trouver
// dans une commune voisine (ex. Novillars pour un habitant de Besançon).
func zoneBonus(f MapFeature, z *UserZone) int {
	if z == nil {
		return 0
	}
	if z.In[f.ID] {
		return 100
	}
	d := haversineKm(z.Center[0], z.Center[1], f.Lng, f.Lat)
	switch {
	case d < 10:
		return 30
	case d < 30:
		return 20
	case d < 60:
		return 10
	}
	return 0
}

// haversineKm estime la distance à vol d'oiseau entre deux points (lng, lat).
func haversineKm(lng1, lat1, lng2, lat2 float64) float64 {
	const r = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLng := toRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// rankFeatures trie par score décroissant, puis par distance croissante
// (le plus proche départage — un usager isolé voit tout de même le plus proche
// en tête), puis par nom. La distance est un bonus de classement, jamais un
// filtre.
func rankFeatures(query string, items []MapFeature, limit int, zone *UserZone) []MapFeature {
	words := significantWords(query)
	score := func(f MapFeature) int {
		return scoreFeature(f.Name, f.Title, f.Address, query, words) + zoneBonus(f, zone)
	}
	dist := func(f MapFeature) float64 {
		if zone == nil {
			return 0
		}
		return haversineKm(zone.Center[0], zone.Center[1], f.Lng, f.Lat)
	}
	sort.SliceStable(items, func(i, j int) bool {
		si, sj := score(items[i]), score(items[j])
		if si != sj {
			return si > sj
		}
		if zone != nil {
			di, dj := dist(items[i]), dist(items[j])
			if di != dj {
				return di < dj
			}
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}
