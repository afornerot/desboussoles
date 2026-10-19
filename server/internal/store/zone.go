package store

import (
	"context"
)

// UserZone porte la zone géographique de l'utilisateur (commune du
// questionnaire) : les entités qu'elle CONTIENT — les villes sont des
// polygones, pas des points — et son centroïde pour le repli par distance.
type UserZone struct {
	Name   string
	In     map[int]bool  // ids des entités contenues dans la zone
	Center [2]float64    // lng, lat du centroïde de la zone
}

type zoneKey struct{}

// WithZone place la zone utilisateur dans le contexte (résolue une fois par
// requête, lue par les outils carte).
func WithZone(ctx context.Context, z *UserZone) context.Context {
	return context.WithValue(ctx, zoneKey{}, z)
}

// ZoneFrom récupère la zone du contexte (nil si absente).
func ZoneFrom(ctx context.Context) *UserZone {
	z, _ := ctx.Value(zoneKey{}).(*UserZone)
	return z
}

// UserZoneInfo localise une zone par son nom (commune ou canton) et calcule
// les entités qu'elle contient. Retourne nil si la zone est inconnue.
func (s *Store) UserZoneInfo(ctx context.Context, name string) *UserZone {
	if name == "" {
		return nil
	}
	// 1. La zone elle-même : polygone → centroïde comme référence de distance.
	// Le nom le plus court gagne (« Besançon » plutôt qu'une variante longue).
	var id int
	var lng, lat float64
	err := s.pool.QueryRow(ctx, `
		SELECT f.id, ST_X(ST_Centroid(f.geom)), ST_Y(ST_Centroid(f.geom))
		FROM map_feature f
		JOIN map_category c ON c.id = f.category_id
		WHERE c.code IN ('ville', 'canton')
		  AND immutable_unaccent(f.name) ILIKE '%' || immutable_unaccent($1) || '%'
		ORDER BY length(f.name)
		LIMIT 1`, name).Scan(&id, &lng, &lat)
	if err != nil {
		return nil
	}

	// 2. Les entités contenues dans le polygone (appartenance exacte).
	rows, err := s.pool.Query(ctx, `
		SELECT f.id
		FROM map_feature f, map_feature v
		WHERE v.id = $1 AND ST_Contains(v.geom, f.geom)`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	in := make(map[int]bool)
	for rows.Next() {
		var fid int
		if err := rows.Scan(&fid); err != nil {
			return nil
		}
		in[fid] = true
	}
	if rows.Err() != nil {
		return nil
	}
	return &UserZone{Name: name, In: in, Center: [2]float64{lng, lat}}
}
