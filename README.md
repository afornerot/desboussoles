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

## API Guide (questionnaire)

Le serveur proxyge deux endpoints vers `GUIDE_API_URL` :

- `POST /api/guide/session` → `{GUIDE_API_URL}/sessions` — démarre une session, retourne la première question
- `POST /api/guide/reponse` → `{GUIDE_API_URL}/reponse` — `{session_id, id_question, reponse}` → question suivante

Format de question : `{session_id, id_question, champ, texte, type: choice|texte|commune|textarea, options: [{valeur, libelle}], mandatory}`.
`id_question == "fin"` marque la fin du questionnaire. Bouton « Passer » → `reponse: "passe"`.

### Fake API de dev

Un mock complet (5 questions) est fourni : service `guide-fake` dans `compose.override.yml` (`fake-guide/server.py`), exposé sur le port `8002`. À retirer quand l'API réelle est branchée.

## Charte graphique

`charte-graphique/` contient les tokens machine-readable (`design-tokens.json`), la charte complète (`charte-graphique.md`), les prompts IA (`prompts-ia.md`) et l'aperçu visuel. Les assets (polices Poppins, illustrations, badges) sont dans `app/internal/web/static/`, servis sur `/static/`.

## Structure

```
app/
  cmd/server/          point d'entrée Go
  internal/
    ai/                chat IA (streaming, outils, sessions)
    store/             accès Postgres/PostGIS
    web/               handlers, templates, static (charte)
charte-graphique/      charte graphique complète
fake-guide/            mock API questionnaire (dev)
init.sql               schéma + seed
```
