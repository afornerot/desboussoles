# DesBoussoles

Plateforme de santé mentale du Doubs — lexique, carte des établissements, guide d'orientation et assistant pro, dans l'univers graphique de la campagne **« Salut, comment vas-tu ? »**.

## Fonctionnalités

| Espace | Description | Profil |
|---|---|---|
| **Lexique** | Définitions sourcées des termes de la santé mentale, recherche et pagination | Public + Pro |
| **Carte** | Établissements et structures du Doubs (PostGIS + Mapbox), légende par catégorie, fiche détail | Public + Pro |
| **Guide** | Questionnaire d'orientation pas à pas adossé à une API externe (sessions/réponses) | Public |
| **Ressources** | Numéros utiles, sites de référence, documents — table en base, catégorisée | Public + Pro |
| **ChatPro** | Assistant IA (OpenCode) avec outils lexique + carte + guide_search | Pro |
| **Urgence** | Widget flottant 15 / 17 / 112 / 3114 présent sur toutes les pages | Public + Pro |

La bascule **Grand public / Professionnel** (mémorisée en `localStorage`) adapte navbar et cartes.

## Stack

- **Go 1.25** — serveur unique, templates HTML embeddés, zéro framework front
- **PostgreSQL 16 + PostGIS** — lexique, établissements (GeoJSON), ressources
- **Mapbox GL JS** — carte interactive
- **API LLM OpenAI-compatible** (OpenCode) — ChatPro en streaming SSE
- **Charte graphique** — Poppins, palette verte/orange/jaune, illustrations à encre grattée (voir `charte-graphique/`)

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
| `AI_PROVIDER` / `AI_MODEL` / `AI_API_KEY` / `AI_BASE_URL` | Configuration du ChatPro (absentes = chat désactivé) |

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

Le serveur proxyge deux endpoints vers `GUIDE_API_URL` (service `guide-api`, voir `python/README.md`) :

- `POST /api/guide/session` → `{GUIDE_API_URL}/sessions` — démarre une session, retourne la première question
- `POST /api/guide/reponse` → `{GUIDE_API_URL}/reponse` — `{session_id, id_question, reponse}` → question suivante

Format de question : `{session_id, id_question, champ, texte, type: choice|texte|commune|textarea, options: [{valeur, libelle}], mandatory}`.
`id_question == "fin"` marque la fin du questionnaire (le JSON patient final est renvoyé alors, avec `bloc_crise` en cas de risque). Bouton « Passer » → `reponse: "passe"`.

L'API Guide est implémentée en **Python / FastAPI** dans `python/` : extraction déterministe du texte libre vers un schéma patient, questions pour les seuls champs manquants, détection de crise par lexique. **Une seule instance** (sessions en mémoire, TTL 30 min) — voir `python/README.md`.

## Charte graphique

`charte-graphique/` contient les tokens machine-readable (`design-tokens.json`), la charte complète (`charte-graphique.md`), les prompts IA (`prompts-ia.md`) et l'aperçu visuel. Les assets (polices Poppins, illustrations, badges) sont dans `app/internal/web/static/`, servis sur `/static/`.

## Structure

```
app/
  cmd/server/          point d'entrée Go
  cmd/importcsv/       import CSV retab.fr → map_feature
  cmd/dbtool/          export/import de la base (pg_dump/psql + gzip)
  internal/
    ai/                chat IA (streaming, outils, sessions)
    store/             accès Postgres/PostGIS
    web/               handlers, templates, static (charte)
charte-graphique/      charte graphique complète
python/                API Guide (FastAPI) — questionnaire d'orientation
init.sql               schéma + seed
```
