# DesBoussoles

Plateforme de santé mentale du Doubs — lexique, carte des établissements, guide d'orientation et assistant pro, dans l'univers graphique de la campagne **« Salut, comment vas-tu ? »**.

## Fonctionnalités

| Espace | Description | Profil |
|---|---|---|
| **Lexique** | Définitions sourcées des termes de la santé mentale, recherche et pagination | Public + Pro |
| **Carte** | Établissements et structures du Doubs (PostGIS + Mapbox), légende par catégorie, fiche détail | Public + Pro |
| **Guide** | Questionnaire d'orientation pas à pas adossé à une API externe (sessions/réponses) | Public |
| **Ressources** | Numéros utiles, sites de référence, documents — table en base, catégorisée | Public + Pro |
| **Chat** | Assistant IA grand public : orientation vers les structures du guide, établissements vérifiés sur la carte | Public |
| **ChatPro** | Assistant IA (Groq) avec outils lexique + carte + guide_search | Pro |
| **Urgence** | Widget flottant 15 / 17 / 112 / 3114 présent sur toutes les pages | Public + Pro |

La bascule **Grand public / Professionnel** (mémorisée en `localStorage`) adapte navbar et cartes.

## Stack

- **Go 1.25** — serveur unique, templates HTML embeddés, zéro framework front
- **PostgreSQL 16 + PostGIS** — lexique, établissements (GeoJSON), ressources
- **Mapbox GL JS** — carte interactive
- **API LLM OpenAI-compatible** (Groq, fallback Gemini/OpenCode) — chats IA en streaming SSE
- **Charte graphique** — Poppins, palette verte/orange/jaune, illustrations à encre grattée (voir `docs/charte-graphique/`)

## Démarrage

```bash
docker compose up -d --build
# → http://localhost:8080
```

Le premier démarrage exécute `init.sql` (schéma + seed). Les variables locales (secrets, tokens) vont dans `compose.override.yml` (ignoré par git).

## Variables d'environnement

| Variable | Rôle |
|---|---|
| `DATABASE_URL` | Connexion Postgres |
| `LISTEN_ADDR` | Adresse d'écoute (défaut `:8080`) |
| `MAPBOX_TOKEN` | Token Mapbox pour la carte |
| `GUIDE_API_URL` | URL de base de l'API questionnaire (Guide) — proxy via `/api/guide/*` |
| `AI_PROVIDER` | `groq`, `gemini` ou `opencode` (client OpenAI-compat) — tout autre valeur bascule sur le SDK genai |
| `AI_MODEL` / `AI_API_KEY` / `AI_BASE_URL` | Modèle, clé et endpoint du fournisseur LLM (absentes = chat désactivé) |
| `AI_MAX_REQUEST_TOKENS` | Plafond de tokens par requête (surcharge la sonde/profil du provider) |
| `AI_REQUESTS_PER_DAY` | Quota de requêtes/jour (surcharge la sonde/profil — borne le nombre d'itérations d'outils) |
| `GUIDE_FILE` / `AMOXTLI_BIN` / `AMOXTLI_DIR` | Fichier du guide à indexer (défaut `/data/guide.md`), binaire amoxtli (défaut `amoxtli`) et workspace d'index (défaut `/data/amoxtli`) |

## Chat IA

Deux espaces, même moteur (`server/internal/ai/`), streaming SSE :

- **Chat grand public** (`/chat`) : orientation vers les structures du Guide, en deux passes — une passe *guide* (`guide_search`, `lexique_search`) qui rédige la recommandation, puis une passe *carte* (`map_search`, `map_nearby`) qui vérifie les établissements mentionnés et alimente le panneau latéral.
- **ChatPro** (`/chatpro`) : une seule passe avec tous les outils, y compris `geocode` (adresse → GPS) et `map_within`.

### Outils du modèle

| Outil | Rôle |
|---|---|
| `lexique_search` | définitions du lexique santé mentale (~306 termes) |
| `guide_search` | recherche dans le Guide des partenaires (indexé par amoxtli) |
| `map_search` / `map_nearby` / `map_within` | établissements, cantons, villes (PostGIS) |
| `geocode` | adresse → coordonnées (API BAN) |

### Budget adaptatif

Les limites du fournisseur LLM sont détectées **à l'amorçage** du service, sans valeurs en dur :

1. variables `AI_MAX_REQUEST_TOKENS` / `AI_REQUESTS_PER_DAY` (prioritaires) ;
2. **sonde** d'un appel minimal lisant les en-têtes `x-ratelimit-limit-tokens` (TPM) et `x-ratelimit-limit-requests` (RPD) ;
3. profils connus par provider (ex. Groq gratuit : 8K TPM / 1K RPD) ;
4. défaut généreux.

Le budget dérive ensuite la taille de l'historique, celle des résultats d'outils et le nombre maximal d'itérations — la ligne `budget LLM adapté` des logs résume les valeurs retenues.

### Garantie de réponse

L'utilisateur reçoit **toujours** une réponse utile : si la boucle d'outils s'épuise, une synthèse finale est forcée sans outils ; si le fournisseur est en échec (quota, panne), un message d'orientation prend le relais (DAC au 03 81 25 05 30, urgences 15/17/112/3114). Les détails techniques partent en logs uniquement.

### Fournisseurs LLM

Client OpenAI-compatible maison (`server/internal/ai/openai_compat.go`) partagé par Groq, Gemini et OpenCode — il rejoue notamment les `thought_signature` de Gemini (indispensables dès qu'un modèle thinking appelle des outils). Le contenu utile des appels d'outils est borné pour rester sous les plafonds de tokens (les résultats de `guide_search` sont tronqués).

### Indexation du guide

Au démarrage du container, `data/guide.md` (monté en `/data/guide.md`) est indexé par **amoxtli** dans `/data/amoxtli` (workspace éphémère, reconstruit à chaque boot — `add` est idempotent). L'outil `guide_search` interroge cet index.

## Sauvegarde de la base (dbtool)

Le container web embarque `dbtool`, une CLI d'export/import de la base PostgreSQL
(`pg_dump` / `psql` + gzip). Le format est du SQL complet : schéma, données et objets
PostGIS, avec `--clean --if-exists` pour être réimportable en remplacement.

```bash
# Export — dump gzip sur stdout
docker exec boussole-web dbtool export > boussole.sql.gz

# Export vers un fichier (-no-gzip pour du SQL brut)
docker exec boussole-web dbtool export -out /data/boussole.sql.gz

# Simulation de restauration (transaction annulée → base inchangée)
docker exec -i boussole-web dbtool import -dry-run < boussole.sql.gz

# Restauration (transaction unique, tout-ou-rien)
docker exec -i boussole-web dbtool import < boussole.sql.gz
```

- L'import accepte indifféremment un `.gz` ou du SQL brut (détection automatique) ;
- `-schema-only` / `-data-only` limitent l'export ; `-no-transaction` désactive l'enveloppe
  transactionnelle de l'import ; `-dsn` cible une autre base ;
- Les journaux vont sur **stderr** : la redirection stdout reste un flux gzip propre.
- Fichier écrit dans le container ? Récupérez-le avec
  `docker cp boussole-web:/data/boussole.sql.gz .`

## API Guide (questionnaire)

Le serveur proxyge deux endpoints vers `GUIDE_API_URL` (service `guide-api`, voir `api-guide/README.md`) :

- `POST /api/guide/session` → `{GUIDE_API_URL}/sessions` — démarre une session, retourne la première question
- `POST /api/guide/reponse` → `{GUIDE_API_URL}/reponse` — `{session_id, id_question, reponse}` → question suivante

Format de question : `{session_id, id_question, champ, texte, type: choice|texte|commune|textarea, options: [{valeur, libelle}], mandatory}`.
`id_question == "fin"` marque la fin du questionnaire (le JSON patient final est renvoyé alors, avec `bloc_crise` en cas de risque). Bouton « Passer » → `reponse: "passe"`.

L'API Guide est implémentée en **Python / FastAPI** dans `api-guide/` : extraction déterministe du texte libre vers un schéma patient, questions pour les seuls champs manquants, détection de crise par lexique. **Une seule instance** (sessions en mémoire, TTL 30 min) — voir `api-guide/README.md`.

## Données & outils

- `data/etablissement_retab.csv` — export RETAB.fr des établissements. Import en base avec la CLI `importcsv` :
  ```bash
  docker cp data/etablissement_retab.csv boussole-web:/data/etablissement_retab.csv
  docker exec boussole-web importcsv -csv /data/etablissement_retab.csv -dry-run   # simulation
  docker exec boussole-web importcsv -csv /data/etablissement_retab.csv            # idempotent, remplace la catégorie etablissement
  ```
- `data/etablissement_guide.csv` — établissements extraits des fiches du guide, au même format. Régénéré par
  `python api-guide/generer_csv_guide.py` (géocodage BAN inclus, `--no-geocode` pour aller vite).
- `data/guide.md` — Guide des partenaires (source de `guide_search`) ; `data/lexique.md` — sources du lexique.
- `docs/guide.docx` — document Word d'origine de `guide.md`.

## Tests

- `server/` : `go test ./...` — budget adaptatif, garantie de réponse (fallback), round-trip `thought_signature` Gemini
- `api-guide/` : `python -m pytest` (tests hors-ligne) et `python scenarios_jsontest.py` (rejeu de parcours → `jsontest/*.json`)

## Charte graphique

`docs/charte-graphique/` contient les tokens machine-readable (`design-tokens.json`), la charte complète (`charte-graphique.md`), les prompts IA (`prompts-ia.md`) et l'aperçu visuel. Les assets (polices Poppins, illustrations, badges) sont dans `server/internal/web/static/`, servis sur `/static/`.

## Structure

```
server/                serveur Go + CLI (build Docker)
  cmd/server/          point d'entrée Go
  cmd/importcsv/       import CSV retab.fr → map_feature
  cmd/dbtool/          export/import de la base (pg_dump/psql + gzip)
  internal/
    ai/                chat IA (budget provider, streaming, outils, sessions)
    store/             accès Postgres/PostGIS
    web/               handlers, templates, static (charte)
api-guide/             API Guide (FastAPI) — questionnaire d'orientation
data/                  données : guide.md, lexique.md, CSV établissements
docs/                  livrables documentaires
  charte-graphique/    charte complète (tokens, prompts, aperçu)
  wordpress/           thème WordPress DesBoussoles
dumps/                 sauvegardes pg_dump (dbtool)
volume/                données Postgres (non versionnées)
init.sql               schéma + seed
docker-compose.yml     services : postgres, boussole, guide-api
```
