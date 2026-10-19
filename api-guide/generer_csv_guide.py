#!/usr/bin/env python3
"""Génère data/etablissement_guide.csv à partir des fiches de data/guide.md (PTSM Doubs).

Le CSV produit a le même format qu'etablissement_retab.csv (37 colonnes, « ; », BOM
UTF-8) mais se base exclusivement sur les établissements décrits dans le guide.

Usage :
    api-guide/generer_csv_guide.py [-o data/etablissement_guide.csv] [--guide data/guide.md]
                                [--no-geocode] [--dry-run] [-v]

Le fichier data/etablissement_retab.csv existant n'est jamais touché.
"""
from __future__ import annotations

import argparse
import csv
import json
import re
import sys
import time
import unicodedata
import urllib.parse
import urllib.request
from pathlib import Path

# --- En-tête : identique à etablissement_retab.csv (37 colonnes, même ordre) ---
HEADER = [
    "id", "url", "nom", "type_etablissement", "categorie_recherche",
    "adresse", "code_postal", "ville", "adresse_complete", "telephone",
    "email", "site_web", "latitude", "longitude",
    "infos_verifiees_par_etablissement", "Public", "Orientation MDPH",
    "admission", "presentation", "prestations", "Personnes concernées",
    "FINESS géographique", "FINESS juridique", "Gestionnaire",
    "Secteur d’activité", "Année d’ouverture", "mdph_competente", "Horaires",
    "Équipements et accessibilité", "Accompagnement et autonomie",
    "Professionnels présents", "Prise en charge",
    "Modalités de prise en charge", "secteur_codes", "secteur_nb_localites",
    "secteur_localites_couvertes", "Soins proposés",
]

# --- Nettoyage HTML / markdown ---
RE_HTML_TAG = re.compile(r"<[^>]+>")
RE_MD_LINK = re.compile(r"\[([^\]]*)\]\((?:mailto:)?[^)]*\)")
RE_MD_IMG = re.compile(r"<img[^>]*>")
RE_MD_ANCHOR = re.compile(r'<span[^>]*class="anchor"[^>]*></span>')
RE_BOLD = re.compile(r"\*\*(.+?)\*\*", re.DOTALL)
RE_UNDERLINE = re.compile(r"<u>(.*?)</u>", re.DOTALL)

# Labels de champs (minuscules, sans accents) → clé normalisée
FIELD_LABELS = {
    "organisme de rattachement": "organisme",
    "coordonnees": "coordonnees",
    "horaires d'ouverture": "horaires",
    "heures d'ouverture": "horaires",
    "horaires et jours d'ouverture": "horaires",
    "public": "public",
    "public accueilli": "public",
    "public concerne": "public",
    "secteur": "secteur",
    "type de structure": "type_structure",
    "description et offre de soins": "description",
    "description": "description",
    "offre de soins": "description",
    "offre": "description",
    "particularites et specificites": "particularites",
    "particularites": "particularites",
    "modalites de prise en charge": "modalites",
    "modalite de prise en charge": "modalites",
    "modalites d'entree": "modalites",
    "missions": "missions",
    "objectifs": "objectifs",
    "equipe pluridisciplinaire": "equipe",
    "activites proposees": "activites",
    "activite proposees": "activites",
    "capacite": "capacite",
    "adresse": "adresse_bis",
    "prise de rendez-vous": "admission",
    "procedure et criteres d'admission": "admission",
    "criteres d'admission": "admission",
    "perimetre d'intervention": "secteur",
    "zone d'intervention": "secteur",
    "methode et moyens d'intervention": "methode",
    "modalite d'adressage": "admission",
}

RE_TEL = re.compile(
    r"(?:T[ée]l(?:[ée]phone)?\.?)\s*:?\s*"
    r"(\d[\d\s.\-–/]{7,20}\d)",
    re.IGNORECASE,
)
RE_TEL_ANY = re.compile(r"\b0\d(?:[\s.\-]?\d{2}){4}\b")
RE_FAX = re.compile(r"Fax\s*:?\s*([\d\s.\-–]{7,20}\d)", re.IGNORECASE)
RE_EMAIL = re.compile(
    r"(?:Courriel|E-mail|Email|Mail)\s*:?\s*"
    r"(?:<|<u>)?"
    r"([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})",
    re.IGNORECASE,
)
RE_EMAIL_GENERIC = re.compile(
    r"\b([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})\b"
)
RE_SITE = re.compile(
    r"(?:Site internet|Site web|Website|Web)\s*:?\s*"
    r"(?:\[)?((?:https?://)?(?:www\.)?[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})",
    re.IGNORECASE,
)
RE_WEB_MD = re.compile(r"\[([^\]]+)\]\((https?://[^)]+)\)")
RE_CP_VILLE = re.compile(r"\b(\d{5})\s+([A-Za-zÀ-ÿ' \-]+)")
RE_CP_ONLY = re.compile(r"\b(\d{5})\b")

# --- Normalisation de nom (inspirée de importcsv/main.go) ---
SIGLES = {
    "aftc", "adapei", "bapu", "caarud", "cada", "camsp", "cattg", "cattp",
    "ch", "chrs", "chu", "cmp", "cmpp", "cph", "cpo", "csapa", "eab", "eam",
    "eanm", "epsm", "esat", "fam", "fjt", "gem", "hdj", "ime", "itep",
    "lhss", "mah", "mda", "mdph", "mecs", "samsah", "savs", "sessad",
    "suva", "ueros", "ur", "e2a", "fam", "mco", "hp",
}
SPECIAL = {"cespa": "CeSPA"}
PARTICULES = {
    "a", "af", "au", "aux", "c", "ce", "cette", "d", "dans", "de", "des",
    "du", "en", "et", "j", "l", "la", "le", "les", "lorsqu", "m", "n", "o",
    "ou", "par", "pour", "puisqu", "qu", "que", "r", "s", "sur", "t", "un",
    "une", "y",
}


def strip_accents(s: str) -> str:
    return "".join(
        c for c in unicodedata.normalize("NFD", s)
        if unicodedata.category(c) != "Mn"
    )


def normalize_key(s: str) -> str:
    """Normalise un label de champ pour l'appariement."""
    s = strip_accents(s.lower())
    s = s.replace("\u2019", "'").replace("\u2018", "'")
    s = re.sub(r"\s+", " ", s).strip()
    return s


def clean_text(s: str) -> str:
    """Nettoie un fragment markdown/HTML du guide en texte lisible."""
    if not s:
        return ""
    s = RE_MD_IMG.sub("", s)
    s = RE_MD_ANCHOR.sub("", s)
    s = RE_MD_LINK.sub(r"\1", s)  # [texte](url) → texte
    s = RE_UNDERLINE.sub(r"\1", s)
    s = RE_HTML_TAG.sub("", s)  # <span class="mark"> → rien
    s = s.replace("&nbsp;", " ").replace("&amp;", "&")
    s = s.replace("&lt;", "<").replace("&gt;", ">")
    # **bold** → bold (on garde le texte)
    s = RE_BOLD.sub(r"\1", s)
    # Nettoyage des espaces multiples et sauts de ligne
    s = re.sub(r"[ \t]+", " ", s)
    s = re.sub(r"\n{3,}", "\n\n", s)
    return s.strip()


def capitalize(s: str) -> str:
    if not s:
        return s
    return s[0].upper() + s[1:].lower()


def capitalize_segments(s: str) -> str:
    return "-".join(capitalize(seg) for seg in s.split("-"))


def normalize_word(w: str, first: bool) -> str:
    w = w.strip()
    if not w:
        return w
    # Isoler la ponctuation de bord (parenthèses, points, virgules)
    lead = w[: len(w) - len(w.lstrip("([./,;"))]
    core = w[len(lead): len(w) - len(w.rstrip(")].,;"))] if len(w) > len(lead) else w
    core = w[len(lead):]
    trail = core[len(core.rstrip(")].,;")):]
    core = core[: len(core) - len(trail)]

    core_lower = core.lower().replace("\u2019", "'")

    # Acronyme tout en capitales dans la source (CHN, CMP, EHPAD…) → conservé
    if len(core) >= 2 and core.isupper() and core.isalpha():
        return lead + core + trail

    if core_lower in SPECIAL:
        return lead + SPECIAL[core_lower] + trail
    if core_lower in SIGLES:
        return lead + strip_accents(core).upper() + trail

    # Apostrophe
    parts = re.split(r"(['/])", core, maxsplit=1)
    if len(parts) == 3:
        head, sep, rest = parts
        head_l = head.lower()
        if head_l in PARTICULES:
            if len(head_l) == 1 and head_l != "d":
                core = head_l.upper() + sep + rest
            elif first:
                core = head_l.upper() + sep + rest
            else:
                core = head_l + sep + rest
        else:
            core = capitalize(head_l) + sep + capitalize_segments(rest.lower())
        return lead + core + trail

    if not first and core_lower in PARTICULES:
        return lead + core_lower + trail
    return lead + capitalize_segments(core_lower) + trail


def normalize_name(s: str) -> str:
    words = s.split()
    return " ".join(
        normalize_word(w, i == 0) for i, w in enumerate(words) if w.strip()
    )


def slug(s: str) -> str:
    s = strip_accents(s.lower())
    s = re.sub(r"[^a-z0-9]+", "-", s).strip("-")
    return s[:60]


# --- Lecture du guide ---
def lire_guide(path: Path) -> list[str]:
    return path.read_text(encoding="utf-8").splitlines(keepends=True)


def truncature_annexes(lines: list[str]) -> list[str]:
    """Tronque le guide avant les annexes (SYNTHESE PAR TERRITOIRE)."""
    out = []
    for line in lines:
        # On s'arrête aux titres de niveau 1 des annexes
        if re.match(r"^#\s+(?:<span[^>]*>)?SYNTHESE", line, re.IGNORECASE):
            break
        if re.match(r"^#\s+(?:<span[^>]*>)?CARTOGRAPHIE", line, re.IGNORECASE):
            break
        if re.match(r"^#\s+(?:<span[^>]*>)?INFORMATIONS COMPLEMENTAIRES",
                    line, re.IGNORECASE):
            break
        if re.match(r"^#\s+(?:<span[^>]*>)?LISTE ABREVIATION",
                    line, re.IGNORECASE):
            break
        if re.match(r"^#\s+(?:<span[^>]*>)?SECTORISATION", line, re.IGNORECASE):
            break
        if re.match(r"^#\s+(?:<span[^>]*>)?DOCUMENTS DE COMMUNICATION",
                    line, re.IGNORECASE):
            break
        out.append(line)
    return out


def decouper_fiches(lines: list[str]) -> list[dict]:
    """Découpe le corps principal en fiches (titre ## ou ### + contenu)."""
    fiches = []
    current = None
    section_courante = ""  # section thématique (# titre)
    for line in lines:
        m_h1 = re.match(r"^#\s+(.+)", line)
        if m_h1 and not re.match(r"^#{2,}", line):
            # Titre de section thématique (PARCOURS ...)
            section_courante = clean_text(m_h1.group(1))
            continue
        m = re.match(r"^(#{2,3})\s+(.+)", line)
        if m:
            if current:
                fiches.append(current)
            titre = clean_text(m.group(2))
            # Nettoyer les restes d'ancres/HTML dans le titre
            titre = re.sub(r"\s+", " ", titre).strip()
            current = {
                "titre": titre,
                "niveau": len(m.group(1)),
                "section": section_courante,
                "lignes": [],
            }
        elif current is not None:
            current["lignes"].append(line)
    if current:
        fiches.append(current)
    return fiches


def est_fiche(f: dict) -> bool:
    """Vérifie qu'une fiche contient bien des champs (pas un simple titre)."""
    contenu = "\n".join(f["lignes"])
    # Doit contenir au moins un label de champ en gras
    return bool(re.search(r"\*\*[A-Za-zÉÈÀÔÛÇa-zéèêàôûç]", contenu))


# --- Extraction des champs ---
def extraire_champs(f: dict) -> dict:
    """Extrait les champs d'une fiche à partir de ses lignes."""
    contenu = "\n".join(f["lignes"])
    champs: dict[str, str] = {}

    # Découper en segments par label gras
    # Patterns de label : **LABEL :** ou **LABEL** ou **<span...>LABEL :</span>**
    label_re = re.compile(
        r"\*\*(?:<span[^>]*>)?\s*"
        r"([A-Za-zÉÈÀÔÛÇa-zéèêàôûç][^*:\n]{1,60}?)\s*"
        r"(?:</span>)?\s*:?\s*\*\*\s*:?\s*",
    )

    # Trouver tous les labels et leurs positions
    matches = list(label_re.finditer(contenu))
    for i, m in enumerate(matches):
        label_brut = m.group(1)
        label = normalize_key(label_brut)
        # Valeur = texte entre ce label et le suivant
        start = m.end()
        end = matches[i + 1].start() if i + 1 < len(matches) else len(contenu)
        valeur = clean_text(contenu[start:end]).strip()
        # Si la valeur est vide, prendre la ligne suivante (label seul sur ligne)
        if not valeur:
            # Chercher sur la ligne suivante dans le contenu brut
            reste = contenu[m.end():].lstrip("\n")
            premiere_ligne = reste.split("\n")[0] if reste else ""
            if not premiere_ligne.startswith("**"):
                valeur = clean_text(premiere_ligne).strip()
        cle = FIELD_LABELS.get(label)
        if cle and valeur:
            # Concaténer si le champ apparaît plusieurs fois
            if cle in champs and champs[cle]:
                champs[cle] += "\n" + valeur
            else:
                champs[cle] = valeur

    return champs


def extraire_coordonnees(f: dict) -> dict:
    """Extrait téléphone, email, site web, adresse d'une fiche."""
    contenu = "\n".join(f["lignes"])
    nettoye = clean_text(contenu)

    result = {
        "telephone": "", "email": "", "site_web": "",
        "adresse": "", "code_postal": "", "ville": "",
    }

    # Téléphone : prendre tous les numéros après « Tél »
    tels = []
    for m in RE_TEL.finditer(nettoye):
        num = m.group(1).strip()
        # Nettoyer les caractères parasites
        num = re.sub(r"[\-–/]+$", "", num).strip()
        num = re.sub(r"\s*\(.*?\)\s*", " ", num).strip()
        if len(re.sub(r"\D", "", num)) >= 9:
            tels.append(num)
    if not tels:
        # Fallback : premier numéro téléphone détecté
        m = RE_TEL_ANY.search(nettoye)
        if m:
            tels.append(m.group(0).strip())
    result["telephone"] = " - ".join(tels[:3])

    # Email
    m = RE_EMAIL.search(nettoye)
    if m:
        result["email"] = m.group(1).strip()
    else:
        # Email Word cassé : [texte](mailto:x@y.z)@[domaine]
        m_mangled = re.search(
            r"\[([^\]]+)\]\(mailto:([^@]+)@([^)]+)\)@?\[?([^\]\s<]*)\]?",
            contenu,
        )
        if m_mangled:
            user = m_mangled.group(2)
            domain = m_mangled.group(3)
            result["email"] = f"{user}@{domain}"
        else:
            m2 = RE_EMAIL_GENERIC.search(nettoye)
            if m2:
                result["email"] = m2.group(1)

    # Site web
    m = RE_SITE.search(nettoye)
    if m:
        url = m.group(1)
        if not url.startswith("http"):
            url = "http://" + url
        result["site_web"] = url
    else:
        m_md = RE_WEB_MD.search(nettoye)
        if m_md:
            result["site_web"] = m_md.group(2)

    # Adresse : ligne contenant un code postal (25xxx)
    adresse_complete = ""
    for ligne in nettoye.split("\n"):
        if RE_CP_VILLE.search(ligne) and not ligne.startswith("Tél"):
            adresse_complete = ligne.strip()
            break
    if not adresse_complete:
        # Chercher une ligne avec pattern rue + CP
        for ligne in nettoye.split("\n"):
            if RE_CP_ONLY.search(ligne) and re.search(
                r"rue|avenue|chemin|place|boulevard|impasse|all[ée]e|quai",
                ligne, re.IGNORECASE,
            ):
                adresse_complete = ligne.strip()
                break

    if adresse_complete:
        m_cp = RE_CP_VILLE.search(adresse_complete)
        if m_cp:
            result["code_postal"] = m_cp.group(1)
            result["ville"] = m_cp.group(2).strip()
            # Adresse = tout avant le code postal
            result["adresse"] = adresse_complete[: m_cp.start()].strip(" ,-")
        else:
            m_only = RE_CP_ONLY.search(adresse_complete)
            if m_only:
                result["code_postal"] = m_only.group(1)
                result["adresse"] = adresse_complete
    else:
        # Dernier recours : ville dans tout le contenu
        m_cp = RE_CP_VILLE.search(nettoye)
        if m_cp:
            result["code_postal"] = m_cp.group(1)
            result["ville"] = m_cp.group(2).strip()

    result["adresse_complete"] = adresse_complete or ""
    return result


# --- Sous-catégories (tags utilisés par la carte / les outils LLM) ---
# Mapping section PARCOURS → tags
SECTION_SUBCATS = {
    "besoin d'écoute ou d'échange": ["ecoute"],
    "offre urgence et pré-post urgence adultes": ["urgences", "adultes"],
    "parcours enfants et adolescents": ["enfants-ados"],
    "parcours périnatalité": ["ambulatoire", "consultation"],
    "parcours adulte general": ["adultes"],
    "parcours trouble de l'humeur, depression": ["adultes", "therapeutique"],
    "parcours addiction": ["adultes", "ambulatoire"],
    "parcours soins ambulatoires": ["ambulatoire", "consultation", "adultes"],
    "parcours precarite": ["precarite", "social"],
    "parcours victimologie / violences intra-familiales (vif)": [
        "adultes", "accompagnement",
    ],
    "parcours specifique": ["accompagnement"],
    "parcours personnes agees": ["adultes"],
    "parcours social": ["social", "precarite"],
    "parcours hebergement": ["hebergement"],
    "parcours somatique": ["ambulatoire", "consultation"],
    "parcours de retablissement psychosocial": [
        "therapeutique", "accompagnement",
    ],
    "dispositif de repit": ["jour", "therapeutique"],
}

# Mapping mots-clés type_de_structure → tags supplémentaires
TYPE_SUBCATS = [
    (r"cmp\b|médico.?psychologique", ["ambulatoire", "consultation"]),
    (r"cmpp", ["ambulatoire", "consultation", "enfants-ados"]),
    (r"cattp|cattg", ["therapeutique", "jour"]),
    (r"h[ôo]pital de jour|hdj", ["jour", "therapeutique"]),
    (r"hospitalisation", ["hospitalisation"]),
    (r"urgences?|upup|capup|samu", ["urgences"]),
    (r"chrs|hébergement|foyer|residence", ["hebergement"]),
    (r"savs|écoute|accueil écoute", ["ecoute"]),
    (r"csapa|caarud|addict", ["ambulatoire"]),
    (r"camsp", ["enfants-ados", "therapeutique"]),
    (r"gem\b|entraide", ["accompagnement"]),
    (r"mda|adolescent", ["enfants-ados"]),
    (r"e2a|esat|insertion", ["social"]),
    (r"cada|asile", ["hebergement", "precarite"]),
    (r"psychiatrie", ["hospitalisation", "adultes"]),
    (r"pédopsy|pedopsy|enfant", ["enfants-ados"]),
    (r"rehabilitation|réhabilitation", ["rehabilitation"]),
    (r"autisme", ["autisme"]),
    (r"bapu|consultation", ["consultation"]),
]


def extraire_subcategories(section: str, type_structure: str) -> list[str]:
    """Déduit les sous-cartégories à partir de la section et du type."""
    tags: list[str] = []
    sec_key = normalize_key(section)
    for k, v in SECTION_SUBCATS.items():
        if k in sec_key:
            tags.extend(v)
            break
    ts = normalize_key(type_structure) if type_structure else ""
    if ts:
        for pattern, extra in TYPE_SUBCATS:
            if re.search(pattern, ts, re.IGNORECASE):
                tags.extend(extra)
    # Dédupliquer en préservant l'ordre
    seen: set[str] = set()
    out: list[str] = []
    for t in tags:
        if t not in seen:
            seen.add(t)
            out.append(t)
    return out


def ecrire_mapping_subcategories(rows: list[dict[str, str]]) -> None:
    """Produit le SQL d'insertion dans etab_subcat_mapping pour les
    catégories du guide (une ligne par combinaison catégorie→tags)."""
    mapping: dict[str, list[str]] = {}
    for r in rows:
        cat = r.get("categorie_recherche", "").strip()
        if not cat:
            continue
        # Reconstruire les tags depuis les colonnes du CSV
        tags: list[str] = []
        sec_key = normalize_key(cat)
        for k, v in SECTION_SUBCATS.items():
            if k in sec_key:
                tags.extend(v)
                break
        ts = normalize_key(r.get("type_etablissement", ""))
        if ts:
            for pattern, extra in TYPE_SUBCATS:
                if re.search(pattern, ts, re.IGNORECASE):
                    tags.extend(extra)
        if tags:
            mapping[cat] = tags
    # Écrire le SQL
    lines = [
        "-- Mapping catégories guide → sous-cartégories (généré)",
        "DELETE FROM etab_subcat_mapping WHERE categorie_recherche IN (",
        "  " + ", ".join(
            "'" + c.replace("'", "''") + "'" for c in mapping
        ),
        ");",
        "INSERT INTO etab_subcat_mapping (categorie_recherche, subcategories) VALUES",
    ]
    values = []
    for cat, tags in mapping.items():
        tags_sql = "'{" + ",".join(tags) + "}'"
        values.append(f"  ('{cat.replace(chr(39), chr(39)*2)}', {tags_sql})")
    lines.append(",\n".join(values) + ";")
    Path("etab_subcat_mapping_guide.sql").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )
    print(f"Mapping SQL écrit : etab_subcat_mapping_guide.sql ({len(mapping)} catégories)")


# --- Géocoding BAN ---
def geocode_ban(adresse: str, cp: str, timeout: float = 8.0) -> tuple[str, str]:
    if not adresse:
        return "", ""
    url = (
        "https://api-adresse.data.gouv.fr/search/?limit=1&q="
        + urllib.parse.quote(adresse)
    )
    if cp:
        url += "&postcode=" + urllib.parse.quote(cp)
    for attempt in range(2):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "boussole"})
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            feats = data.get("features", [])
            if feats:
                coords = feats[0]["geometry"]["coordinates"]
                return str(coords[1]), str(coords[0])
            return "", ""
        except Exception:
            if attempt == 0:
                time.sleep(0.5)
    return "", ""


# --- Assemblage CSV ---
def construire_ligne(f: dict, champs: dict, coord: dict,
                     idx: int, geocoder: bool, log) -> dict[str, str]:
    nom = normalize_name(f["titre"]) if f["titre"] else ""
    if not nom:
        return {}

    ligne = {c: "" for c in HEADER}
    ligne["id"] = str(idx)
    ligne["url"] = "#guide"
    ligne["nom"] = nom
    ligne["infos_verifiees_par_etablissement"] = "non"

    # Type / catégorie
    ligne["type_etablissement"] = clean_text(champs.get("type_structure", ""))
    ligne["categorie_recherche"] = f["section"]

    # Coordonnées
    ligne["adresse"] = coord.get("adresse", "")
    ligne["code_postal"] = coord.get("code_postal", "")
    ligne["ville"] = coord.get("ville", "")
    ligne["adresse_complete"] = coord.get("adresse_complete", "")
    ligne["telephone"] = coord.get("telephone", "")
    ligne["email"] = coord.get("email", "")
    ligne["site_web"] = coord.get("site_web", "")

    # Géocoding BAN
    if geocoder and ligne["adresse_complete"]:
        lat, lon = geocode_ban(
            ligne["adresse_complete"], ligne["code_postal"]
        )
        ligne["latitude"] = lat
        ligne["longitude"] = lon
        if lat:
            log(f"  géocodé: {nom} → {lat},{lon}")

    # Public / personnes concernées
    public = clean_text(champs.get("public", ""))
    ligne["Public"] = public
    ligne["Personnes concernées"] = public

    # Textes longs
    ligne["presentation"] = clean_text(
        champs.get("description", "") or champs.get("objectifs", "")
    )
    ligne["prestations"] = clean_text(
        champs.get("missions", "")
        or champs.get("activites", "")
        or champs.get("particularites", "")
    )
    ligne["admission"] = clean_text(champs.get("admission", ""))
    ligne["Gestionnaire"] = clean_text(champs.get("organisme", ""))
    ligne["Secteur d’activité"] = clean_text(champs.get("secteur", ""))
    ligne["Horaires"] = clean_text(champs.get("horaires", ""))
    ligne["Professionnels présents"] = clean_text(champs.get("equipe", ""))
    ligne["Prise en charge"] = clean_text(champs.get("methode", ""))
    ligne["Modalités de prise en charge"] = clean_text(
        champs.get("modalites", "")
    )
    ligne["Soins proposés"] = clean_text(
        champs.get("description", "") or champs.get("particularites", "")
    )

    return ligne


def main() -> None:
    ap = argparse.ArgumentParser(
        description="Génère un CSV des établissements du guide."
    )
    ap.add_argument(
        "-o", "--output", default="data/etablissement_guide.csv",
        help="fichier CSV de sortie (n'écrase jamais data/etablissement_retab.csv)",
    )
    ap.add_argument(
        "--guide", default="data/guide.md", help="chemin vers guide.md",
    )
    ap.add_argument(
        "--no-geocode", action="store_true",
        help="désactiver le géocodage BAN (plus rapide)",
    )
    ap.add_argument(
        "-v", "--verbose", action="store_true", help="sortie détaillée",
    )
    args = ap.parse_args()

    guide_path = Path(args.guide)
    if not guide_path.exists():
        print(f"ERREUR : guide introuvable : {guide_path}", file=sys.stderr)
        sys.exit(1)

    out_path = Path(args.output)
    # Garde-fou : ne jamais écraser etablissement_retab.csv
    if out_path.resolve() == Path("data/etablissement_retab.csv").resolve():
        print(
            "ERREUR : refus d'écraser data/etablissement_retab.csv — choisissez un "
            "autre nom de fichier.",
            file=sys.stderr,
        )
        sys.exit(1)

    def log(msg: str) -> None:
        if args.verbose:
            print(msg)

    print(f"Lecture de {guide_path} …")
    lines = lire_guide(guide_path)
    lines = truncature_annexes(lines)
    print(f"  {len(lines)} lignes (corps principal, annexes exclues)")

    fiches = decouper_fiches(lines)
    fiches = [f for f in fiches if est_fiche(f)]
    print(f"  {len(fiches)} fiches détectées")

    rows = []
    idx = 0
    sans_coord = 0
    for f in fiches:
        champs = extraire_champs(f)
        coord = extraire_coordonnees(f)
        if not coord["adresse_complete"] and not coord["code_postal"]:
            sans_coord += 1
        idx += 1
        ligne = construire_ligne(
            f, champs, coord, idx, geocoder=not args.no_geocode, log=log,
        )
        if ligne:
            rows.append(ligne)

    print(f"  {len(rows)} lignes générées ({sans_coord} sans adresse)")

    # Écriture CSV (UTF-8 BOM, séparateur « ; »)
    with open(out_path, "w", encoding="utf-8-sig", newline="") as fh:
        writer = csv.DictWriter(
            fh, fieldnames=HEADER, delimiter=";", quoting=csv.QUOTE_MINIMAL,
        )
        writer.writeheader()
        writer.writerows(rows)

    print(f"CSV écrit : {out_path} ({len(rows)} établissements)")

    # Stats de remplissage
    print("\nRemplissage par colonne :")
    for col in HEADER:
        filled = sum(1 for r in rows if r.get(col, "").strip())
        if filled > 0:
            print(f"  {col:45} {filled}/{len(rows)}")


if __name__ == "__main__":
    main()
