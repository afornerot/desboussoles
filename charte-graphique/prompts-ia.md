# Prompts IA — Charte « Salut, comment vas-tu ? »
Version 1.0.0 · à combiner avec `design-tokens.json`

Ces prompts sont calibrés pour **la génération d'images** (DALL·E, Midjourney, Stable Diffusion, Flux) et pour **les agents multimodaux** (compréhension / recréation de style).

---

## 1. Prompt de style de base (à préfixer à chaque génération)

```
Flat vector illustration with hand-drawn irregular black ink outlines, solid color fills,
no gradients, no shadows, no 3D, warm inclusive French public-health campaign style.
Diverse characters (teenagers and adults of different skin tones, ages, genders, body types,
glasses, headphones, hijabs, canes), minimal facial features (dot eyes, simple curved mouths,
thin eyebrows). Bold friendly poses, dialogue gestures (open hand, facing each other).
Typography: Poppins ExtraBold, sentence case, black text.
Palette: green #009850, orange #E85820, pale yellow #F4EFA6, sky blue #C8E8F8,
lilac #D2C0DE, peach #E0A880, cream #F0E8D8, black outlines #181818.
Composition: characters anchored at the bottom edge, flat plain background,
generous negative space for text, 16:9 aspect ratio.
```

## 2. Prompt négatif

```
gradient, drop shadow, glossy, photorealism, 3D render, smooth perfect vector curves,
stock photo, corporate memphis, pixel art, anime, manga, western-generic single ethnicity,
cluttered background, text watermark, low contrast text, neon colors.
```

---

## 3. Déclinaisons par usage

### 3.1 Scène de dialogue (couverture)

```
[style de base] Two teenagers facing each other in conversation, a hand-drawn rounded
speech bubble between them with the words "salut, comment vas-tu ?" inside,
split background: left band peach #E0A880, right area cream #E8E0D8.
Teen girl with red-orange hair and olive green sweater, teen boy with grey-blue hair,
round glasses, pink sweater. Title above in black Poppins ExtraBold.
```

### 3.2 Scène de groupe / inclusion

```
[style de base] Wide composition on sky-blue #C8E8F8 background: one lonely teenager
isolated on the left third, a united group of 8 diverse teenagers chatting on the right,
empty space between them. Illustrates social isolation vs belonging.
Characters cut off by the bottom edge of the frame.
```

### 3.3 Scène multigénérationnelle

```
[style de base] Wide composition on pale yellow #F0E8A0 background: one isolated elderly
woman with grey-blue hair, black jacket, green top, arms crossed on the left;
a multigenerational crowd (child, teens, adults, elder with cane, woman with hijab)
gathered on the right. Inclusion and mental-health conversation theme.
```

### 3.4 Portrait de personnage (fond transparent)

```
[style de base] Single character bust, three-quarter view, transparent background,
one solid color sweater, hand-drawn black outline, calm or thoughtful expression,
PNG with alpha channel, no scene, no props except glasses/headphones if specified.
```

### 3.5 Badge / pictogramme

```
[style de base] Hand-drawn imperfect circle badge, solid fill color #009850 (or #E85820,
#D0C0E0), thick irregular black outline, centered black check mark (or cross, question mark),
label below in black Poppins ExtraBold lowercase: "vous êtes d'accord".
White background, no shadow, no gradient, slightly wobbly circle.
```

### 3.6 Bulle de parole

```
[style de base] Hand-drawn rounded organic speech bubble, solid fill #F0C078,
thick irregular black outline, no tail or small irregular tail,
no text inside, white background, slightly wobbly shape.
```

---

## 4. Consignes pour agents multimodaux (analyse / recréation)

Quand vous analysez ou recréez un visuel de cette campagne, vérifiez dans cet ordre :

1. **Palette** — la couleur est-elle dans la liste fermée du `design-tokens.json` ? Sinon, remappez vers la plus proche.
2. **Contour** — y a-t-il un contour noir irrégulier d'épaisseur variable autour de chaque forme ? Si non, c'est hors-style.
3. **Aplats** — aucune zone dégradée ou ombrée n'est autorisée.
4. **Typographie** — Poppins uniquement ; titres ExtraBold sentence case ; libellés de badges en minuscules.
5. **Sémantique couleur** — vert = accord/vrai/oui ; orange = désaccord/faux/non ; lilas = incertitude ; crème = nuance.
6. **Diversité** — un visuel ne doit jamais représenter un groupe homogène (même peau, même âge, même genre).
7. **Accessibilité** — texte toujours sur aplat clair ou en noir sur couleur vive ; jamais de blanc sur vert/orange.

## 5. Chaîne de rappel compacte (system prompt)

```
STYLE: flat hand-drawn illustration, irregular black ink outlines, solid fills only.
PALETTE: #009850 green=agreement, #E85820 orange=disagreement, #F4EFA6 yellow,
#C8E8F8 sky, #D2C0DE lilac=uncertainty, #E0A880 peach, #181818 outlines.
FONT: Poppins ExtraBold titles, Poppins Regular body, lowercase labels on badges.
MOOD: warm, inclusive, diverse, teen mental-health, no gradients, no shadows, no 3D.
```
