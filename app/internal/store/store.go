package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Term struct {
	ID         int
	Term       string
	Definition string
	Sources    []string
}

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) CountTerms(ctx context.Context, q string) (int, error) {
	var n int
	var err error
	if q == "" {
		err = s.pool.QueryRow(ctx, `SELECT count(*) FROM lexique`).Scan(&n)
	} else {
		err = s.pool.QueryRow(ctx,
			`SELECT count(*) FROM lexique WHERE term ILIKE $1 OR definition ILIKE $1`,
			"%"+q+"%",
		).Scan(&n)
	}
	return n, err
}

func (s *Store) ListTerms(ctx context.Context, q string, offset, limit int) ([]Term, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if q == "" {
		rows, err = s.pool.Query(ctx,
			`SELECT id, term, definition, sources FROM lexique ORDER BY term LIMIT $1 OFFSET $2`,
			limit, offset,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT id, term, definition, sources FROM lexique
			 WHERE term ILIKE $1 OR definition ILIKE $1
			 ORDER BY term LIMIT $2 OFFSET $3`,
			"%"+q+"%", limit, offset,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTerms(rows)
}

func (s *Store) GetTerm(ctx context.Context, id int) (*Term, error) {
	var t Term
	err := s.pool.QueryRow(ctx,
		`SELECT id, term, definition, sources FROM lexique WHERE id = $1`, id,
	).Scan(&t.ID, &t.Term, &t.Definition, &t.Sources)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanTerms(rows pgx.Rows) ([]Term, error) {
	var out []Term
	for rows.Next() {
		var t Term
		if err := rows.Scan(&t.ID, &t.Term, &t.Definition, &t.Sources); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type MapCategory struct {
	ID          int     `json:"id"`
	Code        string  `json:"code"`
	Label       string  `json:"label"`
	GeomKind    string  `json:"geom_kind"`
	Color       *string `json:"color,omitempty"`
	StrokeColor *string `json:"stroke_color,omitempty"`
	PinColor    *string `json:"pin_color,omitempty"`
	PinIcon     *string `json:"pin_icon,omitempty"`
	SortOrder   int     `json:"sort_order"`
}

func (s *Store) ListMapCategories(ctx context.Context) ([]MapCategory, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, code, label, geom_kind, color, stroke_color, pin_color, pin_icon, sort_order
		 FROM map_category ORDER BY sort_order, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapCategory
	for rows.Next() {
		var c MapCategory
		if err := rows.Scan(&c.ID, &c.Code, &c.Label, &c.GeomKind, &c.Color, &c.StrokeColor, &c.PinColor, &c.PinIcon, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) MapFeaturesGeoJSON(ctx context.Context, category string) ([]byte, error) {
	const q = `
		SELECT jsonb_build_object(
			'type', 'FeatureCollection',
			'features', COALESCE(jsonb_agg(jsonb_build_object(
				'type', 'Feature',
				'id', f.id,
				'geometry', ST_AsGeoJSON(f.geom)::jsonb,
				'properties', jsonb_build_object(
					'id', f.id,
					'name', f.name,
					'code', f.code,
					'category', c.code,
					'category_label', c.label,
					'color', c.color,
					'stroke_color', c.stroke_color,
					'pin_color', c.pin_color,
					'pin_icon', c.pin_icon,
					'props', f.props
				)
			)), '[]'::jsonb)
		)
		FROM map_feature f
		JOIN map_category c ON c.id = f.category_id
		WHERE ($1 = '' OR c.code = $1)
	`
	var b []byte
	if err := s.pool.QueryRow(ctx, q, category).Scan(&b); err != nil {
		return nil, err
	}
	return b, nil
}

type FeatureDetail struct {
	ID    int                    `json:"id"`
	Name  string                 `json:"name"`
	Props map[string]interface{} `json:"props"`
}

func (s *Store) GetFeatureDetail(ctx context.Context, id int) (*FeatureDetail, error) {
	var f FeatureDetail
	var propsJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, props FROM map_feature WHERE id = $1`, id,
	).Scan(&f.ID, &f.Name, &propsJSON)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(propsJSON, &f.Props); err != nil {
		return nil, err
	}
	return &f, nil
}

type MapFeature struct {
	ID            int      `json:"id"`
	Name          string   `json:"name"`
	CategoryCode  string   `json:"category"`
	Title         string   `json:"title,omitempty"`
	Address       string   `json:"address,omitempty"`
	Phone         string   `json:"phone,omitempty"`
	Subcategories []string `json:"subcategories,omitempty"`
	Lng           float64  `json:"lng,omitempty"`
	Lat           float64  `json:"lat,omitempty"`
}

const mapFeatureCols = `
	f.id, f.name, c.code,
	COALESCE(f.props->>'title', ''),
	COALESCE(f.props->>'address', ''),
	COALESCE(f.props->>'phone', ''),
	(SELECT COALESCE(array_agg(s #>> '{}'), '{}')
	 FROM jsonb_array_elements(COALESCE(f.props->'subcategories', '[]'::jsonb)) s),
	ST_X(ST_Centroid(f.geom)),
	ST_Y(ST_Centroid(f.geom))
`

func scanMapFeature(row pgx.Row) (MapFeature, error) {
	var f MapFeature
	err := row.Scan(&f.ID, &f.Name, &f.CategoryCode, &f.Title, &f.Address, &f.Phone, &f.Subcategories, &f.Lng, &f.Lat)
	return f, err
}

// SearchMapFeatures recherche par nom + catégorie + sous-catégories.
func (s *Store) SearchMapFeatures(ctx context.Context, query, category string, subcats []string, limit int) ([]MapFeature, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+mapFeatureCols+`
		FROM map_feature f
		JOIN map_category c ON c.id = f.category_id
		WHERE ($1 = '' OR f.name ILIKE '%' || $1 || '%' OR f.props->>'title' ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR c.code = $2)
		  AND ($3::text[] IS NULL OR f.props->'subcategories' ?| $3::text[])
		ORDER BY f.name
		LIMIT $4`,
		query, category, subcats, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapFeature
	for rows.Next() {
		f, err := scanMapFeature(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SearchMapNearby retourne les entités dans un rayon (km) autour d'un point, triées par distance.
func (s *Store) SearchMapNearby(ctx context.Context, lng, lat, radiusKm float64, category string, subcats []string, limit int) ([]MapFeature, []float64, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+mapFeatureCols+`,
			ST_Distance(f.geom::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) / 1000.0
		FROM map_feature f
		JOIN map_category c ON c.id = f.category_id
		WHERE ST_DWithin(f.geom::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3 * 1000.0)
		  AND ($4 = '' OR c.code = $4)
		  AND ($5::text[] IS NULL OR f.props->'subcategories' ?| $5::text[])
		ORDER BY f.geom <-> ST_SetSRID(ST_MakePoint($1, $2), 4326)
		LIMIT $6`,
		lng, lat, radiusKm, category, subcats, limit,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []MapFeature
	var dists []float64
	for rows.Next() {
		var f MapFeature
		var dist float64
		if err := rows.Scan(&f.ID, &f.Name, &f.CategoryCode, &f.Title, &f.Address, &f.Phone, &f.Subcategories, &f.Lng, &f.Lat, &dist); err != nil {
			return nil, nil, err
		}
		out = append(out, f)
		dists = append(dists, dist)
	}
	return out, dists, rows.Err()
}

// SearchMapWithin retourne les entités contenues dans une zone (nom ou code).
func (s *Store) SearchMapWithin(ctx context.Context, zone, category string, subcats []string, limit int) ([]MapFeature, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+mapFeatureCols+`
		FROM map_feature f
		JOIN map_category c ON c.id = f.category_id
		WHERE ST_Within(f.geom, (
			SELECT z.geom FROM map_feature z
			WHERE z.name ILIKE '%' || $1 || '%' OR z.code = $1
			LIMIT 1
		))
		AND f.category_id <> (SELECT z.category_id FROM map_feature z WHERE z.name ILIKE '%' || $1 || '%' OR z.code = $1 LIMIT 1)
		AND ($2 = '' OR c.code = $2)
		AND ($3::text[] IS NULL OR f.props->'subcategories' ?| $3::text[])
		ORDER BY f.name
		LIMIT $4`,
		zone, category, subcats, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapFeature
	for rows.Next() {
		f, err := scanMapFeature(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Ressource est une entrée de la section Ressources (liens, docs, numéros…).
type Ressource struct {
	ID          int
	Title       string
	Description string
	URL         *string
	Category    string
	Profile     string
}

// ListRessources retourne toutes les ressources triées par ordre puis titre.
func (s *Store) ListRessources(ctx context.Context) ([]Ressource, error) {
	return s.listRessources(ctx, "")
}

// ListRessourcesByCategory retourne les ressources d'une catégorie donnée.
func (s *Store) ListRessourcesByCategory(ctx context.Context, category string) ([]Ressource, error) {
	return s.listRessources(ctx, category)
}

func (s *Store) listRessources(ctx context.Context, category string) ([]Ressource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, description, url, category, profile
		FROM ressource
		WHERE ($1 = '' OR category = $1)
		ORDER BY sort_order, title`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ressource
	for rows.Next() {
		var r Ressource
		if err := rows.Scan(&r.ID, &r.Title, &r.Description, &r.URL, &r.Category, &r.Profile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
