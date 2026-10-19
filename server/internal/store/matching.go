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

// NormalizeName produit une forme comparable : minuscules, sans accents. Les
// points joignent les abréviations (« C.m.p » → « cmp »), les autres signes
// séparent les mots (« médico-psychologique » → « medico psychologique »).
func NormalizeName(s string) string {
	s = strings.ToLower(stripAccents(strings.TrimSpace(s)))
	s = strings.ReplaceAll(s, ".", "") // abréviations : c.m.p → cmp
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9')
	}), " ")
}

// AddressCity retourne la ville d'une adresse : le libellé situé APRÈS le
// code postal (« 17 rue de Morteau, 25300 Pontarlier » → « pontarlier »).
// Un nom de rue comme « rue de Morteau » ne doit pas être pris pour la ville.
func AddressCity(addr string) string {
	n := NormalizeName(addr)
	// Dernier groupe de 5 chiffres = code postal.
	idx := -1
	for i := 0; i+5 <= len(n); i++ {
		if isDigits(n[i : i+5]) && (i == 0 || n[i-1] == ' ') && (i+5 == len(n) || n[i+5] == ' ') {
			idx = i
		}
	}
	if idx >= 0 {
		return strings.TrimSpace(n[idx+5:])
	}
	// Pas de code postal : on retombe sur le dernier mot.
	fields := strings.Fields(n)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SameCity juge si une adresse correspond à la ville annoncée (fautes tolérées).
func SameCity(addr, ville string) bool {
	if strings.TrimSpace(ville) == "" {
		return true
	}
	city := AddressCity(addr)
	if city == "" {
		return false
	}
	return FuzzyContains(city, NormalizeName(ville))
}

// NormalizePhone réduit un numéro à ses chiffres (« 03 81 67 97 00 » et
// « 03.81.67.97.00 » sont identiques).
func NormalizePhone(s string) string {
	return strings.Map(func(r rune) rune {
		if '0' <= r && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// SamePhone juge si deux numéros désignent la même ligne (au moins 9 chiffres
// communs pour éviter les coïncidences de numéros partiels).
func SamePhone(a, b string) bool {
	na, nb := NormalizePhone(a), NormalizePhone(b)
	return len(na) >= 9 && na == nb
}

// matchGenericWords sont les mots du domaine, trop courants pour identifier
// une structure : « Équipe Mobile Adolescents » peut en décrire plusieurs, seuls
// les mots spécifiques (EMAHD, Haut-Doubs) discriminent.
var matchGenericWords = map[string]bool{
	"centre": true, "service": true, "sante": true, "mentale": true,
	"medical": true, "medico": true, "psychologique": true, "psychiatrie": true,
	"psychiatrique": true, "social": true, "accueil": true, "ecoute": true,
	"adulte": true, "adultes": true, "adolescent": true, "adolescents": true,
	"enfant": true, "enfants": true, "jeune": true, "jeunes": true,
	"jour": true, "nuit": true, "soin": true, "soins": true, "suivi": true,
	"prise": true, "charge": true, "accompagnement": true, "orientation": true,
	"consultation": true, "consultations": true, "generale": true, "general": true,
	"hopital": true, "clinique": true, "structure": true, "association": true,
	"dispositif": true, "pole": true, "unite": true, "equipe": true,
	"mobile": true, "filiere": true, "maison": true, "famille": true,
	"prevention": true, "therapie": true, "therapeutique": true,
	"hospitalo": true, "universitaire": true, "referent": true,
	"medecin": true, "psychiatre": true, "psychologue": true, "infirmier": true,
}

// SpecificWordsCovered vérifie que TOUS les mots spécifiques de la requête
// (hors mots génériques du domaine) sont retrouvés dans l'établissement —
// sinon la correspondance n'est qu'une coïncidence de vocabulaire partagé.
func SpecificWordsCovered(query, name, title, address string) bool {
	hay := NormalizeName(name + " " + title + " " + address)
	for _, w := range significantWords(query) {
		if matchGenericWords[w] {
			continue
		}
		if !fuzzyWordMatch(hay, w) {
			return false
		}
	}
	return true
}

// MatchScore évalue la correspondance brute entre une requête et un
// établissement (sans bonus de zone) — sert à décider si une correspondance
// est crédible avant de l'afficher.
func MatchScore(query, name, title, address string) int {
	return scoreFeature(name, title, address, query, significantWords(query))
}

// significantWords découpe une requête en mots comparables (normalisés sans
// ponctuation, ≥ 3 caractères — les sigles CMP, CHN, EHPAD comptent —, hors
// mots-outils).
func significantWords(query string) []string {
	fields := strings.Fields(NormalizeName(query))
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, f := range fields {
		if len(f) < 3 || matchStopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// fuzzyWordMatch tolère les fautes de frappe : la chaîne correspond si elle
// est contenue dans le texte, ou si l'un des mots du texte lui ressemble
// (distance d'édition ≤ 20 %). « Poutarlier » retrouve « Pontarlier ».
func fuzzyWordMatch(hay, w string) bool {
	if strings.Contains(hay, w) {
		return true
	}
	for _, hw := range strings.FieldsFunc(hay, func(r rune) bool {
		return !('a' <= r && r <= 'z' || 'à' <= r && r <= 'ÿ')
	}) {
		if similarWord(hw, w) {
			return true
		}
	}
	return false
}

// FuzzyContains vérifie qu'une chaîne (ville, nom) apparaît dans le texte,
// fautes de frappe tolérées. Chaque mot doit y être retrouvé.
func FuzzyContains(hay, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return true
	}
	if strings.Contains(hay, needle) {
		return true
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(needle), func(r rune) bool {
		return !('a' <= r && r <= 'z' || 'à' <= r && r <= 'ÿ')
	}) {
		if !fuzzyWordMatch(hay, w) {
			return false
		}
	}
	return true
}

// similarWord juge deux mots proches : distance d'édition (≤ 20 % de la
// longueur, au moins 1) ou racine commune d'au moins 5 lettres (« infantile »
// ↔ « infanto » partagent « infant »).
func similarWord(a, b string) bool {
	if a == b {
		return true
	}
	// Racine commune : les mots dérivés d'une même racine se ressemblent.
	if len(a) >= 6 && len(b) >= 6 && commonPrefix(a, b) >= 5 {
		return true
	}
	maxDist := len(a) / 4
	if maxDist < 1 {
		maxDist = 1
	}
	if maxDist > 2 {
		maxDist = 2
	}
	return levenshtein(a, b) <= maxDist
}

// commonPrefix renvoie la longueur du préfixe commun de deux chaînes.
func commonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// levenshtein calcule la distance d'édition entre deux chaînes.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// scoreFeature note la proximité entre la requête et un établissement.
// Un mot retrouvé tel quel vaut mieux qu'un mot flou (« CMP » désigne mieux
// « C.m.p » que « CMPP »), qui vaut mieux qu'une simple sous-chaîne.
func scoreFeature(name, title, address, query string, words []string) int {
	hay := NormalizeName(name + " " + title + " " + address)
	q := NormalizeName(query)
	score := 0
	if q != "" && strings.Contains(hay, q) {
		score += 100
	}
	if q != "" && len(q) >= 4 && strings.Contains(q, hay) {
		score += 50
	}
	hayWords := strings.Fields(hay)
	for _, w := range words {
		switch {
		case containsWord(hayWords, w):
			score += 10
		case fuzzyWordMatch(hay, w):
			score += 8
		case strings.Contains(hay, w):
			score += 7
		}
	}
	return score
}

// containsWord vérifie qu'un mot figure dans la liste, entier.
func containsWord(hayWords []string, w string) bool {
	for _, hw := range hayWords {
		if hw == w {
			return true
		}
	}
	return false
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
