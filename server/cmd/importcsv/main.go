// Commande importcsv importe data/etablissement_retab.csv (retab.fr) dans la table
// map_feature, catégorie "etablissement".
//
// Usage :
//
//	importcsv -csv /data/etablissement_retab.csv [-dsn $DATABASE_URL] [-dry-run]
//
// Idempotent : remplace toutes les features de la catégorie à chaque exécution.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sigles conservés en majuscules lors de la normalisation des noms.
var sigles = map[string]bool{
	"aftc": true, "adapei": true, "bapu": true, "caarud": true, "cada": true,
	"camsp": true, "cattg": true, "catt p": false, "cattp": true, "ch": true,
	"chrs": true, "chu": true, "cmp": true, "cmpp": true, "cph": true,
	"cpo": true, "csapa": true, "eab": true, "eam": true, "eanm": true,
	"epsm": true, "esat": true, "fam": true, "fjt": true, "gem": true,
	"hdj": true, "ime": true, "itep": true, "lhss": true, "mah": true,
	"mda": true, "mdph": true, "mecs": true, "samsah": true, "savs": true,
	"sessad": true, "suva": true, "ueros": true, "ur": true,
}

// special casse conservée (sigles non plats).
var special = map[string]string{
	"cespa": "CeSPA",
}

// particules laissées en minuscules (sauf en début de nom).
var particules = map[string]bool{
	"a": true, "af": true, "au": true, "aux": true, "c": true, "ce": true,
	"cette": true, "d": true, "dans": true, "de": true, "des": true,
	"du": true, "en": true, "et": true, "j": true, "l": true, "la": true,
	"le": true, "les": true, "lorsqu": true, "m": true, "n": true,
	"ou": true, "par": true, "pour": true, "puisqu": true, "qu": true,
	"que": true, "s": true, "sur": true, "un": true, "une": true,
}

type record map[string]string

type feature struct {
	name     string
	code     string
	lat      float64
	lon      float64
	props    map[string]interface{}
	geocoded bool
}

func main() {
	csvPath := flag.String("csv", "data/etablissement_retab.csv", "chemin du CSV retab")
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "DSN PostgreSQL (ou $DATABASE_URL)")
	dryRun := flag.Bool("dry-run", false, "analyser sans écrire en base")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	if *dsn == "" {
		*dsn = "postgres://boussoule:changeme@localhost:5432/boussole?sslmode=disable"
	}

	f, err := os.Open(*csvPath)
	if err != nil {
		log.Error("ouverture CSV", "path", *csvPath, "err", err)
		os.Exit(1)
	}
	defer f.Close()

	records, err := readCSV(f)
	if err != nil {
		log.Error("lecture CSV", "err", err)
		os.Exit(1)
	}
	log.Info("CSV lu", "lignes", len(records))

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Error("connexion base", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Error("ping base", "err", err)
		os.Exit(1)
	}

	var catID int
	if err := pool.QueryRow(ctx,
		`SELECT id FROM map_category WHERE code = 'etablissement'`,
	).Scan(&catID); err != nil {
		log.Error("catégorie etablissement introuvable", "err", err)
		os.Exit(1)
	}

	subcatsByCat, err := loadSubcatMapping(ctx, pool)
	if err != nil {
		log.Error("chargement mapping subcategories", "err", err)
		os.Exit(1)
	}

	httpClient := &http.Client{Timeout: 8 * time.Second}

	features, stats := buildFeatures(ctx, log, records, subcatsByCat, httpClient)
	log.Info("features construites",
		"total", stats.total, "insérées", stats.inserted,
		"géocodées", stats.geocoded, "rejetées", stats.skipped,
		"sans_subcats", stats.noSubcats,
	)

	if *dryRun {
		log.Info("dry-run : aucune écriture")
		return
	}

	if err := replaceFeatures(ctx, pool, catID, features); err != nil {
		log.Error("import", "err", err)
		os.Exit(1)
	}
	log.Info("import terminé", "features", len(features))
}

func readCSV(r io.Reader) ([]record, error) {
	cr := csv.NewReader(r)
	cr.Comma = ';'
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("en-tête: %w", err)
	}
	for i, h := range header {
		header[i] = strings.TrimPrefix(strings.TrimSpace(h), "\ufeff")
	}

	var out []record
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ligne %d: %w", len(out)+2, err)
		}
		rec := make(record, len(header))
		for i, h := range header {
			if i < len(row) {
				rec[h] = strings.TrimSpace(row[i])
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

func loadSubcatMapping(ctx context.Context, pool *pgxpool.Pool) (map[string][]string, error) {
	rows, err := pool.Query(ctx, `SELECT categorie_recherche, subcategories FROM etab_subcat_mapping`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var cat string
		var subs []string
		if err := rows.Scan(&cat, &subs); err != nil {
			return nil, err
		}
		out[normalizeKey(cat)] = subs
	}
	return out, rows.Err()
}

// normalizeKey normalise les apostrophes/tirets pour un appariement insensible
// aux variantes typographiques du CSV.
func normalizeKey(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "\u2019", "'")
	s = strings.ReplaceAll(s, "\u2018", "'")
	s = strings.ReplaceAll(s, "\u2013", "-")
	s = strings.ReplaceAll(s, "\u2014", "-")
	return strings.TrimSpace(s)
}

type stats struct {
	total     int
	inserted  int
	skipped   int
	geocoded  int
	noSubcats int
}

func buildFeatures(ctx context.Context, log *slog.Logger, records []record, subcatsByCat map[string][]string, httpClient *http.Client) ([]feature, stats) {
	var st stats
	var out []feature
	for i, rec := range records {
		st.total++
		id := rec["id"]
		name := normalizeName(rec["nom"])
		if id == "" || name == "" {
			log.Warn("ligne ignorée (id ou nom vide)", "ligne", i+2)
			st.skipped++
			continue
		}

		lat, errLat := strconv.ParseFloat(rec["latitude"], 64)
		lon, errLon := strconv.ParseFloat(rec["longitude"], 64)
		geocoded := false
		if errLat != nil || errLon != nil || (lat == 0 && lon == 0) {
			glat, glon, ok := geocodeBAN(ctx, httpClient, rec["adresse_complete"], rec["code_postal"], log, id)
			if !ok {
				log.Warn("ligne ignorée (coordonnées invalides et géocodage échoué)",
					"id", id, "nom", name, "adresse", rec["adresse_complete"])
				st.skipped++
				continue
			}
			lat, lon = glat, glon
			geocoded = true
			st.geocoded++
		}

		categorie := rec["categorie_recherche"]
		subcats := subcatsByCat[normalizeKey(categorie)]
		if len(subcats) == 0 {
			st.noSubcats++
			log.Warn("catégorie sans mapping subcategories", "id", id, "categorie", categorie)
		}

		out = append(out, feature{
			name:     name,
			code:     "retab:" + id,
			lat:      lat,
			lon:      lon,
			geocoded: geocoded,
			props:    buildProps(rec, name, subcats),
		})
		st.inserted++
	}
	return out, st
}

func geocodeBAN(ctx context.Context, client *http.Client, adresse, cp string, log *slog.Logger, id string) (lat, lon float64, ok bool) {
	if adresse == "" {
		return 0, 0, false
	}
	u := "https://api-adresse.data.gouv.fr/search/?limit=1&q=" + url.QueryEscape(adresse)
	if cp != "" {
		u += "&postcode=" + url.QueryEscape(cp)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return 0, 0, false
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		var parsed struct {
			Features []struct {
				Geometry struct {
					Coordinates []float64 `json:"coordinates"`
				} `json:"geometry"`
			} `json:"features"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			lastErr = err
			continue
		}
		if len(parsed.Features) == 0 || len(parsed.Features[0].Geometry.Coordinates) < 2 {
			log.Warn("géocodage sans résultat", "id", id, "adresse", adresse)
			return 0, 0, false
		}
		coords := parsed.Features[0].Geometry.Coordinates
		log.Info("géocodé via BAN", "id", id, "adresse", adresse, "lat", coords[1], "lon", coords[0])
		return coords[1], coords[0], true
	}
	log.Warn("géocodage BAN en échec", "id", id, "adresse", adresse, "err", lastErr)
	return 0, 0, false
}

// buildProps assemble le JSONB props à partir d'une ligne CSV. Seules les
// valeurs non vides sont stockées.
func buildProps(rec record, title string, subcats []string) map[string]interface{} {
	// Provenance : les fiches du guide sont marquées « guide » (url "#guide"),
	// les autres viennent de l'export retab.fr.
	source := "retab.fr"
	if strings.HasPrefix(rec["url"], "#guide") {
		source = "guide"
	}
	props := map[string]interface{}{
		"title":      title,
		"source":     source,
		"source_url": rec["url"],
		"categorie":  rec["categorie_recherche"],
	}

	set := func(key, val string) {
		if val != "" {
			props[key] = val
		}
	}

	set("type_etablissement", rec["type_etablissement"])
	set("address", rec["adresse_complete"])
	set("phone", rec["telephone"])
	set("email", rec["email"])

	website := rec["site_web"]
	if website == "" {
		website = rec["url"]
	}
	set("website", website)

	set("public", rec["Public"])
	set("admission", rec["admission"])
	set("prestations", rec["prestations"])
	set("personnes_concernees", rec["Personnes concernées"])
	set("orientation_mdph", rec["Orientation MDPH"])
	set("mdph_competente", rec["mdph_competente"])
	set("finess_geo", rec["FINESS géographique"])
	set("finess_juridique", rec["FINESS juridique"])
	set("gestionnaire", rec["Gestionnaire"])
	set("secteur_activite", rec["Secteur d’activité"])
	set("annee_ouverture", rec["Année d’ouverture"])
	set("horaires", rec["Horaires"])
	set("equipements", rec["Équipements et accessibilité"])
	set("accompagnement_autonomie", rec["Accompagnement et autonomie"])
	set("professionnels", rec["Professionnels présents"])
	set("prise_en_charge", rec["Prise en charge"])
	set("modalites_prise_en_charge", rec["Modalités de prise en charge"])
	set("soins_proposes", rec["Soins proposés"])
	set("secteur_codes", rec["secteur_codes"])
	set("secteur_nb_localites", rec["secteur_nb_localites"])
	set("secteur_localites_couvertes", rec["secteur_localites_couvertes"])
	if v := rec["infos_verifiees_par_etablissement"]; v != "" {
		props["infos_verifiees"] = v
	}

	if len(subcats) > 0 {
		props["subcategories"] = subcats
	}
	if md := buildContentMD(rec); md != "" {
		props["content_md"] = md
	}
	return props
}

// buildContentMD génère le contenu markdown de la fiche (sections présentes
// uniquement si renseignées dans le CSV).
func buildContentMD(rec record) string {
	var b strings.Builder

	section(&b, "Presentation", paragraphs(rec["presentation"]))
	sectionList(&b, "Public concerné", rec["Public"], "·")
	section(&b, "Admission", paragraphs(rec["admission"]))
	sectionList(&b, "Prestations", rec["prestations"], ";")
	sectionList(&b, "Personnes concernées", rec["Personnes concernées"], ";")
	section(&b, "Horaires", paragraphs(rec["Horaires"]))
	sectionList(&b, "Prise en charge", rec["Prise en charge"], ";")
	sectionList(&b, "Modalités de prise en charge", rec["Modalités de prise en charge"], ";")
	sectionList(&b, "Soins proposés", rec["Soins proposés"], ";")
	sectionList(&b, "Équipements et accessibilité", rec["Équipements et accessibilité"], ";")
	sectionList(&b, "Accompagnement et autonomie", rec["Accompagnement et autonomie"], ";")
	sectionList(&b, "Professionnels présents", rec["Professionnels présents"], ";")

	if b.Len() > 0 {
		b.WriteString("\n")
	}
	if u := rec["url"]; u != "" {
		fmt.Fprintf(&b, "**Fiche source :** [%s](%s)\n", "retab.fr", u)
	}
	return strings.TrimSpace(b.String())
}

func section(b *strings.Builder, title, body string) {
	if body == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n%s\n\n", title, body)
}

func sectionList(b *strings.Builder, title, raw, seps string) {
	items := splitList(raw, seps)
	if len(items) == 0 {
		return
	}
	if len(items) == 1 {
		section(b, title, items[0])
		return
	}
	var sb strings.Builder
	for _, it := range items {
		fmt.Fprintf(&sb, "- %s\n", it)
	}
	section(b, title, strings.TrimSpace(sb.String()))
}

func splitList(raw, seps string) []string {
	if raw == "" {
		return nil
	}
	repl := raw
	for _, sep := range strings.Split(seps, "") {
		repl = strings.ReplaceAll(repl, sep, "\n")
	}
	var out []string
	for _, line := range strings.Split(repl, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func paragraphs(s string) string {
	return strings.TrimSpace(s)
}

func replaceFeatures(ctx context.Context, pool *pgxpool.Pool, catID int, features []feature) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM map_feature WHERE category_id = $1`, catID,
	); err != nil {
		return fmt.Errorf("purge catégorie: %w", err)
	}

	const insertSQL = `
		INSERT INTO map_feature (category_id, name, code, geom, props)
		VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($4, $5), 4326)::geometry, $6::jsonb)`

	for _, f := range features {
		propsJSON, err := json.Marshal(f.props)
		if err != nil {
			return fmt.Errorf("props %s: %w", f.code, err)
		}
		if _, err := tx.Exec(ctx, insertSQL,
			catID, f.name, f.code, f.lon, f.lat, string(propsJSON),
		); err != nil {
			return fmt.Errorf("insert %s: %w", f.code, err)
		}
	}
	return tx.Commit(ctx)
}

// normalizeName convertit un nom en majuscules en casse phrase, en conservant
// les sigles et en gérant les particules françaises et les apostrophes.
func normalizeName(s string) string {
	words := strings.Fields(s)
	out := make([]string, 0, len(words))
	for i, w := range words {
		out = append(out, normalizeWord(w, i == 0))
	}
	return strings.Join(out, " ")
}

func normalizeWord(w string, first bool) string {
	w = strings.TrimSpace(w)
	if w == "" {
		return w
	}
	lower := strings.ToLower(w)
	lower = strings.ReplaceAll(lower, "\u2019", "'")

	if s, ok := special[lower]; ok {
		return s
	}
	if sigles[lower] {
		return strings.ToUpper(stripCaseMarkers(w))
	}

	// Apostrophe : "d'accompagnement", "L'accueil", "qu'il"…
	if parts := strings.SplitN(lower, "'", 2); len(parts) == 2 {
		head, rest := parts[0], parts[1]
		switch {
		case head == "":
			return "'" + capitalizeSegments(rest)
		case particules[head]:
			// Article lié. "l'", "n'", "m'"… prennent une majuscule (L'accueil) ;
			// "d'" reste minuscule en milieu de nom, majuscule en début.
			if len(head) == 1 && head != "d" {
				return strings.ToUpper(head) + "'" + rest
			}
			if first {
				return strings.ToUpper(head) + "'" + rest
			}
			return head + "'" + rest
		default:
			return capitalize(head) + "'" + capitalizeSegments(rest)
		}
	}

	if !first && particules[lower] {
		return lower
	}
	return capitalizeSegments(lower)
}

// capitalizeSegments capitalise chaque segment d'un mot composé
// ("SAINT-JACQUES" -> "Saint-Jacques").
func capitalizeSegments(s string) string {
	segs := strings.FieldsFunc(s, func(r rune) bool { return r == '-' })
	if len(segs) == 0 {
		return s
	}
	var b strings.Builder
	for i, seg := range segs {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(capitalize(seg))
	}
	// Conserve le tiret final éventuel.
	if strings.HasSuffix(s, "-") {
		b.WriteByte('-')
	}
	return b.String()
}

// stripCaseMarkers renvoie le mot sans marques de casse pour re-générer un
// sigle plat (ex. "CeSPA" -> "CESPA").
func stripCaseMarkers(w string) string {
	return strings.Map(unicode.ToLower, w)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	for i := 1; i < len(r); i++ {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}
