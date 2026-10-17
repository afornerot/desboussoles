# API Guide — DesBoussoles (Python / FastAPI)

API du **questionnaire d'orientation** (espace « Guide » de DesBoussoles). Elle pose les questions une à une (âge, adresse, pour qui, durée, impact, besoin…). Elle remplit au fur et à mesure un **JSON patient** structuré, en analysant aussi la réponse libre sur le besoin, puis renvoie le JSON final.

Elle implémente le contrat de `fake-guide/server.py` et peut le remplacer directement : il suffit de renseigner `GUIDE_API_URL`.

> **Démonstrateur : situations fictives uniquement.** L'API ne pose aucun diagnostic et ne donne aucun conseil de soin. Aucun texte utilisateur n'est écrit sur disque (sessions en mémoire).

---

## Sommaire

1. [Démarrage rapide](#1-démarrage-rapide)
2. [Contrat avec le front](#2-contrat-avec-le-front)
3. [Brancher l'API dans DesBoussoles](#3-brancher-lapi-dans-desboussoles)
4. [Le JSON patient final](#4-le-json-patient-final)
5. [Comment ça marche](#5-comment-ça-marche)
6. [Configuration sans toucher au code](#6-configuration-sans-toucher-au-code)
7. [Tests et outils](#7-tests-et-outils)
8. [Structure du dossier](#8-structure-du-dossier)
9. [Points à harmoniser](#9-points-à-harmoniser)

---

## 1. Démarrage rapide

Prérequis : **Python 3.10 ou plus**.

```bash
cd python
python -m venv .venv
# Windows : .venv\Scripts\activate     — Linux/macOS : source .venv/bin/activate
pip install -r requirements-dev.txt
uvicorn app.main:app --reload --port 8000
```

Ouvrir **http://localhost:8000/docs** : documentation interactive (bouton *Try it out* sur chaque route).

Sous Windows, `lancer.bat` fait tout (environnement, installation, lancement, ouverture de `/docs`).

| Fichier | Contenu |
|---|---|
| `requirements.txt` | dépendances d'exécution (FastAPI, Uvicorn, Pydantic, PyYAML) |
| `requirements-dev.txt` | les mêmes + `pytest` et `httpx` pour les tests |

---

## 2. Contrat avec le front

Deux routes suffisent. Chaque question arrive avec le **JSON patient temporaire** (`json_patient`), c'est-à-dire l'état du JSON après la dernière réponse.

### `POST /sessions` — corps vide (ou `{}`)

Crée une session et renvoie la première question :

```json
{
  "session_id": "ded8c3296826f476",
  "id_question": "q_age",
  "type": "texte",
  "champ": "age",
  "texte": "Quel âge a la personne concernée ? (en années)",
  "mandatory": true,
  "options": [],
  "go_chat": false,
  "json_patient": { "...": "JSON patient temporaire (même schéma que le JSON final, §4)" }
}
```

### `POST /reponse` — `{session_id, id_question, reponse}`

```json
{ "session_id": "ded8c3296826f476", "id_question": "q_age", "reponse": "15" }
```

Deux réponses possibles :

- **La question suivante**, au même format que ci-dessus, avec `json_patient` mis à jour.
- **Le JSON patient final**, quand le questionnaire est terminé (complet, ou alerte de crise). On le reconnaît à `"id_question": "fin"` (voir [§4](#4-le-json-patient-final)).

### Déroulé des questions (`config/questions.json`)

| # | `id_question` | Type | Remplit | Posée | Obligatoire |
|---|---|---|---|---|---|
| 1 | `q_age` | **texte** libre (« 12 », « 12 ans », « il a 12 ans », « douze ans »), enregistré en **entier** | `personne.age_ans` (+ `tranche_age` déduite) | si manquant | oui |
| 2 | `q_adresse` | **adresse** | `personne.commune_*`, secteurs | si manquant | oui |
| – | `q_quartier` | choice | `personne.quartier_besancon` | seulement pour un mineur à Besançon | oui |
| 3 | `q_switch` | choice oui/non | `go_chat` | toujours | oui |
| 4 | `q_pour_qui` | choice : moi / un tiers | `demandeur` | si manquant | oui |
| 5 | `q_duree` | choice | `besoin.duree` | toujours | non |
| 6 | `q_impact` | choice | `besoin.impact_domaine` | toujours | non |
| 7 | `q_impact_niveau` | choice | `besoin.impact_niveau` | toujours | non |
| 8 | `q_besoin` | textarea | analysé → parcours, statuts, risque, mots-clés | toujours | non |
| 9 | `q_accompagnement` | choice oui/non | `besoin.deja_suivi` | toujours | non |

- **Pour qui** : « moi » → `demandeur: soi`. « Un tiers » → `demandeur: proche`, ou `professionnel` si le texte libre l'indique (« je suis infirmière scolaire… »).
- **Textes adaptés** : quand la personne écrit pour un tiers, l'API envoie la variante à la troisième personne (« La personne bénéficie-t-elle… » au lieu de « Bénéficiez-vous… »). Les questions posées avant `q_pour_qui` (âge, adresse) utilisent la version directe, faute de savoir encore pour qui on écrit.
- **Adresse** : l'API en extrait la commune, par le code postal ou le nom de la commune ; le dernier cité l'emporte, donc « rue de Morteau, 25000 Besançon » donne Besançon. **L'adresse complète n'est pas conservée**, seule la commune l'est. Hors Doubs, « 21000 Dijon » est acceptée et marquée hors périmètre. Sans commune reconnaissable, ou avec un code postal partagé, la réponse est refusée avec un message pour préciser.
- **Seule saisie libre** : `q_besoin`. C'est elle qui peut déclencher l'alerte (fin immédiate).

### `go_chat` — passage au chatbot (question `q_switch`)

`go_chat` fait partie du **schéma de question**. Il vaut `false` par défaut.

- La question **`q_switch`** demande : « Voulez-vous continuer le questionnaire ou passer à un chatbot ? » (`oui` = passer au chatbot, `non` = continuer).
- Si la réponse à `q_switch` est **`oui`**, toutes les questions suivantes arrivent avec `go_chat: true`. Le JSON patient (`json_patient` et JSON final) porte aussi `go_chat: true`.
- Le questionnaire **ne s'arrête pas** : c'est au front de basculer vers le chat quand il reçoit `go_chat: true`.
- Le front n'envoie jamais `go_chat` : seule la réponse à `q_switch` compte.

`q_switch` ne remplit aucun champ du schéma patient (`champ: null`). Sa place, son texte et ses libellés se règlent dans `config/questions.json` ; elle doit garder l'option de valeur `oui`.

### Types de questions

| `type` | Affichage attendu | Réponse envoyée |
|---|---|---|
| `textarea` | zone de texte libre | le texte (analysé comme une saisie libre) |
| `choice` | boutons (`options[].libelle`) | `options[].valeur` |
| `texte` | champ texte court | le texte |
| `commune` | champ commune (autocomplétion) | nom de la commune ou code postal |
| `adresse` | champ adresse (rue, code postal, ville) | l'adresse ; la commune en est extraite |

### Questions facultatives (`mandatory: false`)

Si la personne passe la question, envoyer `"pass"` (ou `"passe"`, ce qu'envoie le bouton « Passer » actuel, ou une chaîne vide). La valeur `"pass"` est enregistrée dans le JSON patient, et la question n'est plus jamais reposée.

### Erreurs

Le code HTTP est `422` si la réponse est refusée (question obligatoire laissée vide, valeur invalide, code postal partagé par plusieurs communes…) et `404` si la session est inconnue ou expirée :

```json
{
  "error": "code postal partagé par plusieurs communes, précisez : Arçon, Doubs, Pontarlier, …",
  "detail": { "erreur": "…", "question": { "...même question à reposer..." } }
}
```

`error` est le message à afficher. `detail.question` permet de réafficher la même question.

---

## 3. Brancher l'API dans DesBoussoles

Le serveur Go relaie déjà `POST /api/guide/session` vers `GUIDE_API_URL/sessions` et `POST /api/guide/reponse` vers `GUIDE_API_URL/reponse`. Il suffit donc d'ajouter le service et de renseigner la variable.

**`python/Dockerfile` (proposé, non fourni) :**

```dockerfile
FROM python:3.12-slim
WORKDIR /srv
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
EXPOSE 8000
# une seule instance : les sessions sont en mémoire
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
```

**`docker-compose.yml` :**

```yaml
services:
  guide-api:
    build: ./python
    restart: unless-stopped
    # environment:
    #   SEUIL_ALERTE: "3"        # niveau de risque qui interrompt le questionnaire (voir §5)
  app:
    environment:
      GUIDE_API_URL: "http://guide-api:8000"
```

> ⚠️ **Une seule instance / un seul worker** : les sessions sont en mémoire, elles expirent après 30 min d'inactivité. Pour plusieurs instances, il faudrait les stocker ailleurs (Redis, Postgres…).

Les tests de `tests/test_compat_desboussoles.py` vérifient la compatibilité avec `guide.html` : corps `'{}'`, bouton « Passer » qui envoie `"passe"`, erreurs lues dans `error`, communes de toute la France.

---

## 4. Le JSON patient final

Il est renvoyé seul, avec deux clés en plus du schéma patient. Le JSON patient temporaire (`json_patient` des questions) suit le même schéma, sans ces deux clés.

| Clé | Contenu |
|---|---|
| `id_question` | toujours `"fin"` |
| `bloc_crise` | **présent seulement si `risque.niveau ≥ 2`** : texte fixe d'urgence (15, 112, 3114, 17, 3919, 119), à afficher en premier |

Schéma complet : [`config/personne_finale.schema.json`](config/personne_finale.schema.json). Extrait :

```json
{
  "id_question": "fin",
  "session_id": "…",
  "demandeur": { "value": "proche", "confidence": 0.9, "source": "regex", "evidence": "mon fils" },
  "personne": {
    "age_ans": { "value": 15, "confidence": 0.95, "source": "regex" },
    "tranche_age": { "value": "13-17" },
    "commune_nom": { "value": "Morteau", "confidence": 1.0, "source": "utilisateur" },
    "commune_insee": { "value": "25411" },
    "secteur_infanto_juvenile": { "value": "CGI Morteau" },
    "secteur_adulte": { "value": null },
    "hors_perimetre_guide": { "value": false },
    "statuts": []
  },
  "besoin": { "parcours_principal": { "value": "addiction" }, "parcours_secondaires": ["enfant_adolescent"],
              "type_demande": { "value": "aide_pour_un_proche" }, "deja_suivi": { "value": false },
              "duree": { "value": "moins_6_mois" }, "impact_domaine": { "value": "etudes" },
              "impact_niveau": { "value": "beaucoup" } },
  "risque": { "niveau": 0, "indices": [], "negation_detectee": false },
  "mots_cles": [ { "mot": "joints", "origine": "q_besoin", "reperes": ["parcours:addiction"] } ],
  "champs_manquants": [],
  "suivi_questions": { "q_age": "ok", "q_adresse": "ok", "q_switch": "ok", "q_pour_qui": "ok", "q_duree": "ok",
                       "q_impact": "ok", "q_impact_niveau": "ok", "q_besoin": "ok", "q_accompagnement": "pass" },
  "go_chat": false
}
```

Chaque champ suit la même forme `{value, confidence, source, evidence}`. La source vaut `regex`, `gazetteer`, `lexique`, `utilisateur` ou `derive`. La valeur `"pass"` est possible pour une question facultative passée.

| Champ | Utilité pour l'orientation |
|---|---|
| `personne.secteur_adulte` / `secteur_infanto_juvenile` | pôle de psychiatrie adulte / CGI de secteur, tirés des tables du guide PTSM 25. Pour un adulte à Besançon : `BESANCON_A_PRECISER` (sectorisation par rue) |
| `personne.hors_perimetre_guide` | commune hors Doubs, ou dans le pays de Montbéliard |
| `besoin.parcours_principal` / `parcours_secondaires` | `ecoute_soutien`, `humeur_depression`, `addiction`, `enfant_adolescent`, `perinatalite`, `victimologie_vif`, `specifique`… |
| `besoin.duree` | `moins_15_jours`, `moins_1_mois`, `moins_6_mois`, `plus_6_mois` (ou `pass`) |
| `besoin.impact_domaine` / `impact_niveau` | `travail`, `etudes`, `logement`, `famille`, `relations_sociales` / `peu`, `moderement`, `beaucoup` |
| `besoin.deja_suivi` | `true` / `false` : déjà un accompagnement, ou déjà parlé à son médecin (ou `pass`) |
| `risque.niveau` | 0 aucun indice · 1 détresse · 2 idées suicidaires / auto-agression / violences · 3 danger immédiat |
| `mots_cles` | mots écrits par la personne, même hors lexique ; `reperes` vide = mot non reconnu (piste d'enrichissement) |

---

## 5. Comment ça marche

```
POST /sessions → q_age → q_adresse → (q_quartier) → q_switch → q_pour_qui → q_duree → q_impact
              → q_impact_niveau → q_besoin (texte libre) → q_accompagnement
        │  chaque réponse
        ▼
  réponses à boutons / adresse  → champs du JSON patient (la commune et les secteurs sont déduits de l'adresse)
  texte libre (q_besoin)        → normalisation (accents, SMS, nombres en lettres) → extracteurs déterministes :
                                  âge · commune · demandeur · lexique (parcours, statuts, suivi) · CRISE · mots-clés
        ▼
  JSON patient ──► décision (app/decision.py) :
        1. risque ≥ SEUIL_ALERTE (3)  → FIN immédiate (alerte), même si des questions restent
        2. plus aucune question        → FIN (complet)
        3. sinon                       → question suivante
```

- **Champs requis** : âge (ou tranche), commune, demandeur, plus le quartier pour un mineur à Besançon. Leurs questions ne sont posées que s'ils manquent. Le parcours est déduit du texte libre (`q_besoin`).
- **Questions « toujours »** (durée, impact, niveau, accompagnement) et `q_besoin` : posées à tout le monde, facultatives.
- **Les réponses de l'utilisateur priment** sur l'extraction. Exemple : `q_accompagnement = non` l'emporte sur « je vois un psy » dans le texte.
- **Crise** : le lexique est indépendant de tout LLM. La négation est gérée (« pas d'idées noires » abaisse le niveau d'un cran, sans jamais le ramener à 0), ainsi que le discours rapporté (« mon frère s'est suicidé il y a 5 ans » → niveau 1).
- **Pas de LLM** dans cette version : uniquement des règles et des lexiques, ce qui donne une réponse instantanée et ne nécessite ni clé ni envoi de données.

---

## 6. Configuration sans toucher au code

| Fichier | Rôle |
|---|---|
| [`config/questions.json`](config/questions.json) | questions posées : **ordre**, `id_question` unique, `type`, `champ`, `texte` (direct), `texte_tiers` (troisième personne, facultatif), `mandatory`, `toujours` (posée même si le champ est déjà rempli), `options`. Validé au démarrage (le serveur refuse de démarrer si le fichier est incohérent), schéma d'édition `questions.schema.json` |
| [`lexiques/crise.yaml`](lexiques/crise.yaml) | expressions de crise par niveau (regex, accents ignorés). **Toute modification doit être validée cliniquement** |
| [`lexiques/parcours.yaml`](lexiques/parcours.yaml) | expressions courantes → parcours, statuts, type de demande, suivi existant |
| [`lexiques/sms.yaml`](lexiques/sms.yaml) | abréviations SMS |
| [`data/gazetteer.json`](data/gazetteer.json) | communes du Doubs, codes postaux, secteurs adulte / CGI, quartiers de Besançon |

Variables d'environnement :

| Variable | Défaut | Effet |
|---|---|---|
| `SEUIL_ALERTE` | `3` | niveau de risque qui arrête le questionnaire ; `2` pour arrêter aussi sur les idées suicidaires |
| `QUESTIONS_JSON` | `config/questions.json` | autre fichier de questions |

Après une modification des modèles Pydantic, régénérer les schémas JSON : `python -m app.exporter_schemas`.

---

## 7. Tests et outils

```bash
python -m pytest                 # 143 tests, sans réseau ni clé
python scenarios_jsontest.py     # rejoue 5 parcours fictifs → jsontest/*.json (échanges + JSON final)
```

Routes d'outillage (non utilisées par le front) :

| Route | Rôle |
|---|---|
| `GET /sessions/{sid}` | JSON patient en cours de remplissage (débogage) |
| `DELETE /sessions/{sid}` | supprimer une session |
| `GET /questions` | catalogue des questions dans l'ordre |
| `POST /personne/evaluer` | évaluer une fiche patient écrite à la main (exemples dans `exemples/`) |
| `GET /personne/schema` | JSON Schema du patient |

---

## 8. Structure du dossier

```
python/
├── app/
│   ├── main.py             routes FastAPI, sessions, contrat front
│   ├── decision.py         fin du questionnaire (alerte / complet / en cours)
│   ├── fusion.py           construction du JSON patient, dérivations (tranche, secteurs)
│   ├── extracteurs.py      âge, demandeur, crise, lexique
│   ├── gazetteer.py        communes et quartiers
│   ├── motscles.py         mots-clés des réponses libres
│   ├── questions.py        chargement/validation de config/questions.json
│   ├── normalisation.py    accents, SMS, nombres en lettres
│   ├── schema.py           modèles Pydantic (schéma patient)
│   └── exporter_schemas.py export des JSON Schema dans config/
├── config/                 questions.json + JSON Schema du contrat
├── lexiques/  data/        lexiques YAML, gazetteer
├── exemples/  jsontest/    fiches et parcours fictifs
└── tests/
```

---

## 9. Points à harmoniser

1. **Ton** : les questions vouvoient (choix validé). La charte « Salut, comment vas-tu ? » tutoie ; pour changer, il suffit de modifier `texte` / `texte_tiers` dans `config/questions.json`.
2. **Fin du questionnaire** : `guide.html` appelle `showEnd()` sans exploiter le JSON final. Pistes :
   - afficher `bloc_crise` s'il est présent ;
   - utiliser `secteur_*`, `parcours_principal` et `commune_insee` pour pré-filtrer la carte et les ressources.
3. **Urgence** : le widget flottant 15 / 17 / 112 / 3114 et `bloc_crise` se recoupent. Il faudrait un seul texte, validé par la coordination PTSM.
4. **Adresse (`type: "adresse"`)** : `guide.html` ne connaît pas encore ce type. Il affiche donc un simple champ texte, qui fonctionne mais sans autocomplétion. Il faudrait lui ajouter un champ adresse, éventuellement avec l'autocomplétion de l'API Adresse (api-adresse.data.gouv.fr). Les adresses hors Doubs sont acceptées et marquées `hors_perimetre_guide`.
5. **Codes HTTP** : `POST /sessions` répond `201` (le fake répondait `200`). Le front teste `res.ok`, donc c'est compatible.
6. **Langage** : cette API est en Python, alors que le reste de la plateforme est en Go. Elle reste un service séparé derrière `GUIDE_API_URL`.
7. **Valeurs cliniques à valider** : seuil d'alerte (3), niveaux du lexique de crise, numéros du bloc de crise.
8. **Ordre « Pour qui ? »** : la question arrive en 4e position. L'âge est donc demandé de façon générique (« la personne concernée ») et l'adresse en version directe (« votre adresse »), même quand la personne écrit pour un tiers. Placer `q_pour_qui` en premier permettrait d'adapter ces textes aussi.
