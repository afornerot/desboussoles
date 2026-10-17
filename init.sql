CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS postgis_topology;
CREATE EXTENSION IF NOT EXISTS fuzzystrmatch;
CREATE EXTENSION IF NOT EXISTS postgis_tiger_geocoder;
CREATE EXTENSION IF NOT EXISTS unaccent;

CREATE TABLE IF NOT EXISTS lexique (
    id SERIAL PRIMARY KEY,
    term TEXT NOT NULL UNIQUE,
    definition TEXT NOT NULL,
    sources TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION immutable_unaccent(text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS
$$ SELECT unaccent('unaccent', $1) $$;

CREATE INDEX IF NOT EXISTS idx_lexique_term_unaccent ON lexique (immutable_unaccent(term));

CREATE TABLE IF NOT EXISTS map_category (
    id SERIAL PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    geom_kind TEXT NOT NULL CHECK (geom_kind IN ('point', 'zone')),
    color TEXT,
    stroke_color TEXT,
    pin_color TEXT,
    pin_icon TEXT,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS map_feature (
    id SERIAL PRIMARY KEY,
    category_id INT NOT NULL REFERENCES map_category(id),
    name TEXT NOT NULL,
    code TEXT,
    geom geometry(Geometry, 4326) NOT NULL,
    props JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_map_feature_geom_type CHECK (
        ST_GeometryType(geom) IN ('ST_Point', 'ST_MultiPoint', 'ST_Polygon', 'ST_MultiPolygon')
    ),
    CONSTRAINT chk_map_feature_geom_not_empty CHECK (NOT ST_IsEmpty(geom))
);

CREATE INDEX IF NOT EXISTS idx_map_feature_geom ON map_feature USING GIST (geom);
CREATE INDEX IF NOT EXISTS idx_map_feature_category ON map_feature (category_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_map_feature_code ON map_feature (category_id, code) WHERE code IS NOT NULL;

INSERT INTO map_category (code, label, geom_kind, color, stroke_color, pin_color, pin_icon, sort_order)
VALUES
    ('etablissement', 'Établissement', 'point', NULL, NULL, '#7c3aed', 'hospital', 5),
    ('departement', 'Département', 'zone', '#22c55e', '#15803d', NULL, NULL, 10),
    ('canton', 'Canton', 'zone', '#eab308', '#a16207', NULL, NULL, 20),
    ('ville', 'Ville', 'zone', '#ef4444', '#b91c1c', NULL, NULL, 30)
ON CONFLICT (code) DO NOTHING;

-- Mapping categorie_recherche (retab.fr) -> sous-catégories carte (vocabulaire IA).
CREATE TABLE IF NOT EXISTS etab_subcat_mapping (
    categorie_recherche TEXT PRIMARY KEY,
    subcategories TEXT[] NOT NULL DEFAULT '{}'
);

INSERT INTO etab_subcat_mapping (categorie_recherche, subcategories) VALUES
    ('ACT - Appartements de Coordination Thérapeutique', '{therapeutique,ambulatoire,adultes}'),
    ('Appartement Thérapeutique', '{therapeutique,hebergement}'),
    ('Autres dispositifs pour enfants', '{enfants-ados}'),
    ('Autres dispositifs ressources', '{ressource}'),
    ('Autres types de structures et prestations', '{accompagnement}'),
    ('Bureaux d’Aide Psychologique Universitaire (BAPU)', '{consultation,ecoute,jeunes}'),
    ('CAARUD (Centre d''Accueil et d''Accompagnement à la Réduction des risques pour Usagers de Drogues)', '{ecoute,precarite,ambulatoire}'),
    ('CADA (Centre Accueil Demandeurs Asile)', '{hebergement,precarite,adultes}'),
    ('CAMSP - Centres d''Action Médico-Sociale Précoce', '{enfants-ados,ambulatoire,therapeutique}'),
    ('CATTG - Centre d''Activités Thérapeutiques et de Temps de Groupe (ex CATTP)', '{therapeutique,jour,adultes}'),
    ('CATTG - Centre d''Activités Thérapeutiques et de Temps de Groupe (ex CATTP) - Enfant/Adolescent', '{therapeutique,jour,enfants-ados}'),
    ('CH (Centre Hospitalier)', '{hospitalisation,urgences,adultes,enfants-ados}'),
    ('CHRS (Centre d’Hébergement et de Réinsertion Sociale)', '{hebergement,precarite,social}'),
    ('CMP - Centre Médico Psychologique - Enfant/Adolescent et autres lieux de consultation', '{ambulatoire,consultation,enfants-ados}'),
    ('CMP - Centre Médico Psychologique et autres lieux de consultation', '{ambulatoire,consultation,adultes}'),
    ('CMPP - centre Médico-Psycho-Pédagogique', '{therapeutique,enfants-ados,ambulatoire}'),
    ('CPH (Centre Provisoire Hébergement)', '{hebergement,precarite}'),
    ('CPO (Centre de Pré Orientation)', '{jeunes,hebergement}'),
    ('CSAPA (Centre Soins Accompagnement Prévention Addictologie)', '{ambulatoire,accompagnement,precarite}'),
    ('CeSPA - Centre de Soins Post-Aigus (ex centre de postcure)', '{hospitalisation,therapeutique}'),
    ('Centre expert, centre ressource, centre référent, centre support', '{ressource,ecoute}'),
    ('Clinique, maison de santé', '{hospitalisation,therapeutique,adultes}'),
    ('Consultation de psychotraumatologie', '{consultation,therapeutique}'),
    ('Dispositif Un chez soi d''abord ou équivalent', '{ressource,hebergement,adultes}'),
    ('Dispositif proposant des activités durant la journée (Associations, clubs,...)', '{jour,accompagnement}'),
    ('EAM (Etablissement d''Accueil Médicalisé)', '{hebergement,accompagnement,adultes}'),
    ('EANM (Etablissement Accueil Non Médicalisé)', '{social,hebergement,accompagnement,adultes}'),
    ('ESAT (Etablissement et Service d''Aide par le Travail)', '{social,adultes}'),
    ('Equipe de liaison/Equipe mobile', '{accompagnement,ambulatoire}'),
    ('Etablissement d''Accueil Mère-Enfant', '{accompagnement,hebergement}'),
    ('FAM (Foyer d’Accueil Médicalisé)', '{hebergement,adultes,accompagnement}'),
    ('FJT (Foyer de Jeunes Travailleurs)', '{hebergement,jeunes,social}'),
    ('GEM - Groupe d''Entraide Mutuelle', '{accompagnement,social,hebergement}'),
    ('HDJ (Hôpital De Jour)', '{jour,ambulatoire,therapeutique,adultes}'),
    ('HDJ - Hôpital De Jour - Enfant/Adolescent', '{jour,therapeutique,enfants-ados}'),
    ('IME (Institut Médico Educatif)', '{enfants-ados,jeunes,accompagnement}'),
    ('ITEP - Institut Thérapeutique Educatif et Pédagogique', '{therapeutique,enfants-ados,jeunes}'),
    ('LHSS (Lits Halte Soins Santé)', '{hebergement,precarite,ecoute}'),
    ('MAH (Maisons d''Accueil Hospitalières)', '{hebergement,accompagnement}'),
    ('MAS - Maison d''Accueil Spécialisée', '{hebergement,adultes,accompagnement}'),
    ('MDA - Maison des Adolescents', '{ressource,enfants-ados,jeunes}'),
    ('MDPH - Maison Départementale des Personnes Handicapées', '{ressource}'),
    ('MECS (Maison d''Enfants à Caractère Social)', '{enfants-ados,social,hebergement}'),
    ('Maison relais, pension de famille', '{hebergement,social}'),
    ('Mission locale', '{jeunes,social}'),
    ('Résidence Sociale', '{hebergement,social}'),
    ('Résidence accueil', '{hebergement,social}'),
    ('Résidence autonomie', '{hebergement,social,adultes}'),
    ('SAMSAH – Service d''Accompagnement Médico-Social pour Adultes Handicapés', '{ambulatoire,adultes,accompagnement,social}'),
    ('SAVS – Service d’Accompagnement à la Vie Sociale', '{social,accompagnement,adultes}'),
    ('SESSAD - Service d''Éducation Spéciale et de Soins à Domicile', '{accompagnement,enfants-ados,ambulatoire}'),
    ('Service d''urgence', '{urgences}'),
    ('Service d''urgence psychiatrique', '{urgences,hospitalisation}'),
    ('Services et structures de soins spécifiques, autres', '{ambulatoire}'),
    ('Structures et associations de soutien et d''accompagnement aux proches et familles', '{accompagnement,ecoute}'),
    ('Structures et associations de soutien et d''accompagnement aux professionnels de santé', '{accompagnement,ecoute}'),
    ('UEROS (Unités Evaluation Réentraînement et d''Orientation Sociale et Professionnelle)', '{accompagnement,social,adultes}'),
    ('Unité d''hospitalisation Enfant/Adolescent', '{hospitalisation,enfants-ados}'),
    ('Unité de psychogériatrie', '{hospitalisation,adultes}'),
    ('Unité de soins psychiatriques', '{hospitalisation,adultes}'),
    ('Vie quotidienne & loisirs', '{accompagnement}')
ON CONFLICT (categorie_recherche) DO UPDATE SET subcategories = EXCLUDED.subcategories;

-- Ressources : liens, documents, numéros, réseaux sociaux (profil public/pro)
CREATE TABLE IF NOT EXISTS ressource (
    id SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    url TEXT,
    category TEXT NOT NULL CHECK (category IN ('numero','site','document','reseau_social','lien')),
    profile TEXT NOT NULL DEFAULT 'both' CHECK (profile IN ('public','pro','both')),
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_ressource_title ON ressource (title);

INSERT INTO ressource (title, description, url, category, profile, sort_order) VALUES
    ('3114 — Prévention du suicide', 'Numéro national de prévention du suicide, gratuit, anonyme et confidentiel, 24h/24 et 7j/7.', 'https://www.3114.fr', 'numero', 'both', 5),
    ('3120 — Prévention du suicide', 'Numéro national gratuit et anonyme, 24h/24 et 7j/7.', 'https://www.3120.fr', 'numero', 'both', 10),
    ('Fil Santé Jeunes', 'Écoute anonyme pour les 12-25 ans — 0 800 235 236.', 'https://www.filsantejeunes.com', 'numero', 'public', 20),
    ('Drogues Info Service', 'Info et écoute autour des addictions — 0 800 23 23 23.', 'https://www.drogues-info-service.fr', 'numero', 'both', 30),
    ('Glossaire de la santé mentale — Ministère de la Santé', 'Définitions officielles des termes de la santé mentale.', 'https://sante.gouv.fr/prevention-en-sante/sante-mentale/article/glossaire-de-la-sante-mentale', 'site', 'both', 10),
    ('Lexique — Fondation FondaMental', 'Définitions et acronymes, recherche alphabétique.', 'https://www.fondation-fondamental.org/lexique', 'site', 'both', 20),
    ('Dico ViaPsY', 'Termes et acronymes du champ médico-social (CMP, CRA, CSAPA…).', 'https://viapsy.fr/dico', 'site', 'both', 30),
    ('Vocabulaire de la santé mentale — OQLF', 'Nomenclature rigoureuse des troubles (Québec).', 'https://www.oqlf.gouv.qc.ca/ressources/bibliotheque/dictionnaires/vocabulaire-sante-mentale.aspx', 'site', 'pro', 40),
    ('Thésaurus SantéPsy', 'Thésaurus SKOS orienté CIM-10/11 pour les professionnels.', 'https://loterre.istex.fr/2CX/fr/index', 'site', 'pro', 50),
    ('Campagne « Salut, comment vas-tu ? »', 'Visuels, présentations et charte graphique de la campagne de prévention.', NULL, 'document', 'both', 10),
    ('Guide des partenaires santé mentale du Doubs', 'Établissements, structures et contacts — aussi accessible via la carte et ChatPro.', NULL, 'document', 'pro', 20)
ON CONFLICT (title) DO NOTHING;
