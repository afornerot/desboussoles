# Charte graphique — « Salut, comment vas-tu ? »
### Campagne débat mouvant · Santé mentale des adolescents

**Version 1.0.0 — 17 octobre 2026**
Source : extraction automatique des visuels, polices et du thème PowerPoint du répertoire (`EXPORTS/`).
Fichier machine lisible associé : [`design-tokens.json`](./design-tokens.json) — variables CSS : [`palette.css`](./palette.css) — prompts IA : [`prompts-ia.md`](./prompts-ia.md) — aperçu : [`apercu-palette.html`](./apercu-palette.html)

---

## 1. ADN VISUEL

Une **illustration plate à encre grattée** : aplats de couleur francs, contours noirs irréguliers dessinés à la main, formes organiques et personnages expressifs. L'ensemble transmet **bienveillance, inclusion et accessibilité** — un ton de santé publique jeune, jamais clinique.

| Pilier | Traduction visuelle |
|---|---|
| Bienveillance | Rond, arrondi, aplats chauds, absence d'angle vif |
| Inclusion | Diversité des peaux, âges, genres, silhouettes, accessoires |
| Dialogue | Personnages face à face, bulles de parole, gestes ouverts |
| Clarté | Contraste noir/clair maximal, pictogrammes + libellés systématiques |

---

## 2. COULEURS

### 2.1 Couleurs primaires

| Nom | HEX | RGB | Rôle |
|---|---|---|---|
| **Vert accord** | `#009850` | 0, 152, 80 | Validation, « oui », « vrai », accord |
| **Orange désaccord** | `#E85820` | 232, 88, 32 | Rejet, « non », « faux », désaccord |
| **Jaune signature** | `#F4EFA6` | 244, 239, 166 | Fond principal, bulle de dialogue |
| **Jaune vif** | `#EBE353` | 235, 227, 83 | Accent énergique, highlights |
| **Rose** | `#F193BC` | 241, 147, 188 | Accent douceur, vêtements |
| **Vert menthe** | `#B4D6C6` | 180, 214, 198 | Fond secondaire doux |
| **Lilas** | `#D2C0DE` | 210, 192, 222 | Incertitude, neutre |
| **Vert lien** | `#169A53` | 22, 154, 83 | Liens, appels à l'action |

### 2.2 Sémantique des réponses (à respecter strictement)

| Concept | Couleur | Pictogramme | Libellé |
|---|---|---|---|
| Accord / Vrai / Oui | Vert `#009850` | Coche ✔ | « vous êtes d'accord » / « oui » / « vrai » |
| Désaccord / Faux / Non | Orange `#E85820` | Croix ✘ | « vous n'êtes pas d'accord » / « non » / « faux » |
| Incertitude | Lilas `#D0C0E0` | Point d'interrogation ? | « vous ne savez pas » |
| Nuance / difficulté | Crème `#F0E8D8` | — (texte seul) | « effectivement pas si facile » |

> ⚠️ **Jamais de couleur seule** : toujours associée à un pictogramme et un libellé (voir §7 Accessibilité).

### 2.3 Neutres et fonds de scène

| Nom | HEX | Usage |
|---|---|---|
| Encre | `#000000` | Contours des badges, coches, croix |
| Noir doux | `#181818` | Contours des illustrations, vêtements sombres |
| Gris texte | `#404040` | Corps de texte |
| Blanc chaud | `#F4F0EB` | Fond de page adouci |
| Crème | `#F0E8D8` | Pastille neutre, fonds doux |
| Bleu ciel | `#C8E8F8` | Fond de scène « groupe ados » |
| Jaune pâle | `#F0E8A0` | Fond de scène « groupe multigénérationnel » |
| Pêche | `#E0A880` | Bandeau de couverture dialogue |
| Gris clair | `#E8E0D8` | Zone neutre de composition |

### 2.4 Palette illustration (peaux, cheveux, vêtements)

**Peaux** — `#F0C888` · `#E8C8A0` · `#E0A080` · `#C09868` · `#A07850` · `#684838`
**Cheveux** — roux `#E85820` · blond `#E8E050` · chatain `#583830` · brun `#684838` · gris-bleu `#B0B8D8` · violet clair `#D0C0D8` · noir `#181818`
**Vêtements** — vert `#009850` · orange `#E85820` · rose `#E89890` / `#F090B8` · bleu `#58B0D8` · lilas `#B0B8D8` · kaki `#505018` · olive `#406050` · jaune `#E8E050` · moutarde `#F0C888` · crème `#F0E0B8` · noir `#181818`

---

## 3. TYPOGRAPHIE

**Famille unique : Poppins** (fichiers fournis dans `EXPORTS/TYPOGRAPHIES/`, v4.004, licence OFL)

| Rôle | Graisse | Casse | Exemple |
|---|---|---|---|
| Titre de campagne | ExtraBold (800) | Sentence | *Et si on parlait de notre santé mentale ?* |
| Sous-titre | Bold (700) | Sentence | Questions de débat |
| Corps de texte | Regular (400) | Sentence | Paragraphes, légendes |
| Légende d'icône | ExtraBold (800) | **Minuscules** | *vous êtes d'accord* |
| Citation | Italic (400) | Sentence | Témoignages |

**Échelle recommandée (16:9, 1920×1080)** : Display 64 · H1 48 · H2 36 · H3 28 · Body-L 22 · Body 18 · Caption 14.
**Interlettrage titre** : `-0.01em`. Pas de texte justifié. Titres en sentence case (jamais de MAJUSCULES pleines).

> ⚠️ Les anciennes slides utilisaient aussi *Roboto* / *Roboto Condensed* — à migrer vers Poppins pour l'homogénéité.

---

## 4. STYLE ILLUSTRATION

### 4.1 Règles non négociables

1. **Aplats uniquement** — aucun dégradé, aucune ombre portée, aucun reflet, aucune 3D.
2. **Contours noirs irréguliers** (`#181818`–`#000000`), épaisseur variable (4–10 px selon la taille), tracé feutre tremblé. **Jamais** de courbe vectorielle parfaitement lisse.
3. **Visages minimalistes** : yeux en points ou petits traits, sourcils en arcs, bouche en trait ou ovale.
4. **Composition plate** : pas de perspective, pas de mise en scène volumétrique.
5. **Formes organiques** : bulles de parole dessinées à main levée, badges légèrement irréguliers.

### 4.2 Principes de composition

- **Scène de dialogue** : bandeau vertical pêche `#E0A880` à gauche + zone crème `#E8E0D8` à droite ; personnages ancrés en bas de cadre.
- **Scène de groupe** : personnage isolé à gauche (solitude, exclusion) + groupe uni à droite (appartenance). Fonds bleu ciel ou jaune pâle.
- **Marges de sécurité** : 5 % de la largeur en présentation 16:9, 10 mm en affiche A4.
- **Ancrage** : personnages en bas de cadre, débordement possible hors cadre autorisé.

### 4.3 Diversité obligatoire

Varier systématiquement : teint de peau (6 valeurs), âge (ado → senior), genre, carrure, lunettes, foulard, casque audio, cheveux courts/longs/attachés/viellese, handicap (fauteuil, canne — cf. fond multigénérationnel). **Ne jamais réduire la diversité à un unique stéréotype.**

---

## 5. COMPOSANTS

| Composant | Forme | Fond | Contour | Texte / pictogramme |
|---|---|---|---|---|
| Badge accord | Cercle | `#009850` | `#000000` | Coche noire + « vous êtes d'accord » |
| Badge désaccord | Cercle | `#E85820` | `#000000` | Croix noire + « vous n'êtes pas d'accord » |
| Badge incertitude | Cercle | `#D0C0E0` | `#000000` | « ? » + « vous ne savez pas » |
| Pastille oui / vrai | Ovale | `#009850` | `#000000` | « oui » / « vrai » en Poppins ExtraBold noir |
| Pastille non / faux | Ovale | `#E85820` | `#000000` | « non » / « faux » |
| Pastille nuance | Ovale | `#F0E8D8` | `#000000` | « effectivement pas si facile » |
| Bulle de parole | Forme organique | `#F0C078` (alt. `#F4EFA6`, `#F193BC`) | `#000000` | Poppins, noir |
| QR code | Carré | blanc | — | Noir, lien ressource |

Les assets correspondants sont dans `EXPORTS/VISUELS/` (fichiers `1-*.png`, `2-*.png`, `fond-*.png/jpg`, `perso-questions-*.png`, `qr-code.png`).

---

## 6. SPÉCIFICATIONS TECHNIQUES

| Élément | Valeur |
|---|---|
| Présentation | 16:9 — 1920 × 1080 px |
| Affiche | A4, 300 dpi |
| Export raster | PNG (fonds transparents pour personnages/badges), JPG pour fonds, 144 ppi écran |
| Export vectoriel | SVG, PDF |
| Échelle d'espacement | base 8 px : 8 / 16 / 24 / 40 / 64 / 96 |
| Unité couleur | HEX sRGB |

---

## 7. ACCESSIBILITÉ

- **Contraste** : texte `#404040` ou `#181818` sur fonds clairs → ratio ≥ 7:1. Sur vert `#009850` ou orange `#E85820`, toujours **texte noir** (jamais de blanc).
- **Daltonisme** : ne jamais opposer vert/orange seul ; la coche/croix et le libellé portent toujours l'information.
- **Taille minimale** : 18 px en corps de texte écran ; légendes d'icônes ≥ 14 px.
- **Lisibilité** : pas de texte sur zone chargée d'une illustration ; prévoir un aplat de fond uni derrière le texte.

---

## 8. POURQUOI CES COULEURS ? (lecture sémantique)

- **Vert** = oui, accord, sécurité → codification culturelle immédiate, validée par les badges.
- **Orange** = alerte, désaccord, faux → chaud mais non agressif (contrairement au rouge).
- **Jaune pâle** = bienveillance, espace de parole, chaleur humaine.
- **Bleu ciel** = calme, apaisement, sérénité.
- **Lilas** = hésitation, zone grise, complexité des émotions.
- **Pêche** = intimité du dialogue en tête-à-tête.

---

*Document généré à partir des fichiers sources du projet. Toute évolution de palette doit être reportée dans `design-tokens.json` en priorité (source de vérité machine), puis dans ce document.*
