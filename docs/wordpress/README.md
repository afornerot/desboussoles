# DesBoussoles — thème WordPress

Thème bloc inspiré de **Twenty Twenty-Five 1.5**, habillé avec la **charte graphique
DesBoussoles** (Poppins, palette vert/orange/jaune signature, contours noirs,
aplats sans ombre). Contenu générique : c'est le look qui respecte la charte.

## Installation

```bash
# 1. Empaqueter le thème
cd docs/wordpress
zip -r desboussoles.zip theme-desboussoles -x "*.DS_Store"

# 2. Installer
#    - soit copier theme-desboussoles/ dans wp-content/themes/
#    - soit Apparence > Thèmes > Ajouter > Téléverser desboussoles.zip
# 3. Activer « DesBoussoles »
```

Prérequis : WordPress 6.5+ (recommandé : 6.7), PHP 7.4+.

## Contenu

| Dossier | Rôle |
|---|---|
| `theme.json` | Palette, typo Poppins, espacements, styles de blocs (source de vérité de la charte) |
| `style.css` | Compléments : cartes bordées, bandeaux, focus, mouvement réduit, impression |
| `functions.php` | Supports d'édition, styles de blocs « Carte » / « Bandeau » / « Urgence » |
| `assets/fonts/` | Poppins 400 / 400i / 800 / 800i (SIL OFL) |
| `parts/` | En-tête (bandeau jaune signature) et pied de page |
| `templates/` | index, home, single, page, page-no-title, archive, search, 404 |
| `patterns/` | Couverture, trois cartes, citation, appel à l'action, texte + image |

## La charte dans l'éditeur

- **Couleurs** : palette complète (vert accord, orange urgence, jaune signature,
  jaune vif, rose, menthe, lilas, fonds de scène) dans Couleurs > Palette.
- **Typo** : Poppins et l'échelle de la charte (Display 64 → Légende 14).
- **Styles de blocs** : sur un Groupe → « Carte » (bordure 3px, radius 16px,
  survol jaune) ou « Bandeau » ; sur un Bouton → « Urgence » (orange).
- **Règles tenues** : texte noir sur fonds colorés, aucune ombre, focus visible,
  corps de texte ≥ 18 px.
