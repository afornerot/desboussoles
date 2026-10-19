# Plan — identification des établissements depuis la source canonique

## Contexte (diagnostic validé)

- La carte est alimentée par `data/etablissement_guide.csv` (141 lignes géocodées) : mêmes
  noms / adresses / téléphones que le guide lu par le chat → les données correspondent.
- Le LLM **reformule** les noms (« Centre de Gestion Infantile » au lieu de « Centre de
  Guidance Infanto-Juvénile ») et **fabrique** les téléphones (motif 03 81 25 … emprunté
  au DAC) dans sa réponse :
  - le matching chat → carte casse sur des identifiants inventés ;
  - surtout, **l'usager reçoit de fausses coordonnées**.

## Principe

Le LLM ne fait que CHOISIR les structures recommandées ; les identifiants (nom, ville,
téléphone) sont **recopiés depuis les résultats `guide_search`** (source canonique),
jamais reformulés.

## Étapes d'implémentation

### 1. `server/internal/ai/flow.go` — collecte des extraits du guide

- Dans `Chat()`, près de `seenCalls` : `var guideExcerpts []string`
- Dans la boucle d'outils, après `llm.ExecuteToolCall` :
  `if tc.Name() == "guide_search" { guideExcerpts = append(guideExcerpts, msg.Content()) }`
- `verifyMapEstablishments(ctx, userMsg, response string, guideExcerpts []string, out)`
  → `extractStructures(ctx, userMsg, response, guideExcerpts)`

### 2. `server/internal/ai/prompts.go` — deux consignes

- `SystemPromptExtractNames` : ajouter une section d'entrée « Extraits du guide (source
  canonique) » + « recopie le nom, la ville et le téléphone EXACTS depuis ces extraits ;
  ne reformule jamais, ne devine jamais un numéro ».
- `SystemPromptPublicGuide` et `SystemPromptPro` : « Recopie les noms et coordonnées
  (adresse, téléphone) exactement comme dans le guide — ne les invente ni ne les
  reformule jamais. »

### 3. `extractStructures` — entrée enrichie

- Signature : `extractStructures(ctx, userMsg, response string, guideExcerpts []string)`
- Message utilisateur = `Message de la personne` + `Réponse de l'assistant` +
  `Extraits du guide` (concaténés, bornés ~4 000 caractères pour respecter le budget)

### 4. Tests

- `server/internal/ai/flow_test.go` : extraction d'un extrait de guide factice →
  nom + téléphone recopiés à l'identique (pas de reformulation).
- Vérification réel : comparer les téléphones du panneau à ceux de
  `data/etablissement_guide.csv` (les trois doivent être exacts).

### 5. Cosmétique données (optionnel)

- `server/cmd/importcsv/main.go` : props `source` = `"guide"` (aujourd'hui `"retab.fr"`
  en dur, alors que le contenu vient de `etablissement_guide.csv`).

## Validation attendue

- 3 structures proposées → 3 sur la carte, avec les **vrais** numéros ;
- les coordonnées affichées à l'usager sont celles du guide ;
- 36+ tests au vert, `go build ./... && go vet ./... && go test ./...` ;
- `docker compose up -d --build` puis scénario réel (Pontarlier / Besançon).
