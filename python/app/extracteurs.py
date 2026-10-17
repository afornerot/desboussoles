"""Extracteurs déterministes : âge, demandeur, crise (composant de sécurité), lexique des parcours.

Tous travaillent sur le texte normalisé (voir normalisation.texte).
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import date
from functools import lru_cache

import yaml

from .normalisation import LEXIQUES, fold, tokens

# ============================ ÂGE ============================

RELATIONS = ("petite soeur|petit frere|grande soeur|grand frere|adolescente|adolescent|petit-fils|petite-fille|"
             "grand-mere|grand-pere|belle-mere|beau-pere|fiston|fils|fille|gamine|gamin|gosse|ado|enfant|garcon|"
             "petite|petit|bebe|frere|soeur|mere|pere|maman|papa|mamie|papy|mari|femme|copine|copain|conjointe|"
             "conjoint|compagne|compagnon|neveu|niece|cousine|cousin|amie|ami|pote|voisine|voisin|oncle|tante|"
             "eleve|patiente|patient")
TIERS = ("eleve|patiente|patient|jeune|adolescente|adolescent|ado|fille|garcon|femme|homme|monsieur|dame|"
         "usagere|usager|enfant|residente|resident|personne")

RE_REL_AGE = re.compile(rf"\b(?:mon|ma|notre|son|sa|mes|nos)\s+(?:{RELATIONS})(?:\s+(?:ainee|aine|cadette|cadet))?"
                        r"\s*(?:,|\(|de|qui a|a|agee? de)?\s*(\d{1,3})\s*(?:ans|an|a)\b")
RE_TIERS_AGE = re.compile(rf"\b(?:un|une)\s+(?:{TIERS})\s*(?:,|\(|de|agee? de)?\s*(\d{{1,3}})\s*(?:ans|an)\b")
RE_IL_A = re.compile(r"\b(?:il|elle)\s+a\s+(?:bientot\s+|presque\s+|deja\s+|que\s+|juste\s+)?(\d{1,3})\s*(?:ans|an)\b")
RE_JAI = re.compile(r"\bj'\s?ai\s+(?:bientot\s+|presque\s+|deja\s+|que\s+|juste\s+)?(\d{1,3})\s*(?:ans|an|a)\b")
RE_MOI = re.compile(r"\bmoi\s*,?\s*(\d{1,3})\s*(?:ans|a)\b")
RE_NAISSANCE = re.compile(r"\bnee?\s+(?:en|le)\s+(?:(\d{1,2})[/.-](\d{1,2})[/.-])?((?:19|20)\d{2})\b")
RE_GENERIQUE = re.compile(r"\b(\d{1,3})\s*(?:ans|an)\b|\b(\d{1,3})a\b")
AVANT_DUREE = {"depuis", "pendant", "durant", "fait", "y", "apres", "avant", "dans", "en", "avais", "avait",
               "avaient", "plus", "moins", "bout", "tous", "les", "dernieres"}
RE_APRES_DUREE = re.compile(r"^\s*(?:de\s+(?:mariage|travail|prison|vie|couple|relation|chomage|suivi|retard|"
                            r"carriere|service|anciennete|galere|traitement|therapie)|d'ecart|plus tard|que|qu')")
REGLES_TRANCHE = [
    (re.compile(r"\b(?:maternelle|petite section|moyenne section|grande section)\b"), "3-6", 0.6),
    (re.compile(r"\b(?:en|au|du)\s+(?:cp|ce1|ce2|cm1|cm2)\b|\bprimaire\b"), "7-12", 0.6),
    (re.compile(r"\ben\s+(?:6e|6eme|sixieme|5e|5eme|cinquieme)\b"), "7-12", 0.6),
    (re.compile(r"\ben\s+(?:4e|4eme|quatrieme|3e|3eme|troisieme)\b"), "13-17", 0.6),
    (re.compile(r"\b(?:college|collegien|collegienne)\b"), "13-17", 0.5),
    (re.compile(r"\b(?:lycee|lyceen|lyceenne)\b|\ben\s+(?:seconde|2nde|premiere|1ere|1re|terminale)\b"), "13-17", 0.6),
    (re.compile(r"\b(?:fac|faculte|universite|etudiant|etudiante|bts|iut|prepa|master)\b"), "18-25", 0.6),
    (re.compile(r"\b(?:retraite|retraitee|ehpad|maison de retraite)\b"), "65+", 0.5),
    (re.compile(r"\b(?:mon|ma|notre|son|sa)\s+(?:bebe|nourrisson|nouveau-ne)\b|\bnourrisson\b"), "0-2", 0.6),
]


@dataclass
class CandidatAge:
    age: int
    role: str  # soi | tiers | inconnu
    confidence: float
    evidence: str
    pos: int


@dataclass
class ResultatAge:
    candidats: list[CandidatAge] = field(default_factory=list)
    tranche: str | None = None
    tranche_conf: float = 0.0
    tranche_evidence: str = ""


def _est_duree(txt: str, debut: int, fin: int) -> bool:
    avant = tokens(txt[:debut])[-3:]
    if any(t in AVANT_DUREE for t in avant):
        return True
    return bool(RE_APRES_DUREE.match(txt[fin:]))


def extraire_age(txt: str, aujourdhui: date | None = None) -> ResultatAge:
    aujourdhui = aujourdhui or date.today()
    r, pris = ResultatAge(), []

    def libre(a, b):
        return not any(a < y and b > x for x, y in pris)

    def ajout(m: re.Match, g: int, role: str, conf: float):
        if m.group(g) is None or not libre(m.start(), m.end()):
            return
        v = int(m.group(g))
        if 0 <= v <= 110:
            pris.append((m.start(), m.end()))
            r.candidats.append(CandidatAge(v, role, conf, m.group(0), m.start()))

    for rx, role, conf in ((RE_REL_AGE, "tiers", 0.95), (RE_TIERS_AGE, "tiers", 0.9), (RE_JAI, "soi", 0.95),
                           (RE_MOI, "soi", 0.9), (RE_IL_A, "tiers", 0.9)):
        for m in rx.finditer(txt):
            ajout(m, 1, role, conf)
    for m in RE_NAISSANCE.finditer(txt):
        if not libre(m.start(), m.end()):
            continue
        v = aujourdhui.year - int(m.group(3))
        if m.group(1):
            j, mo = int(m.group(1)), int(m.group(2))
            if (aujourdhui.month, aujourdhui.day) < (mo, j):
                v -= 1
        if 0 <= v <= 110:
            pris.append((m.start(), m.end()))
            r.candidats.append(CandidatAge(v, "inconnu", 0.9, m.group(0), m.start()))
    for m in RE_GENERIQUE.finditer(txt):
        g = 1 if m.group(1) is not None else 2
        if not _est_duree(txt, m.start(), m.end()):
            ajout(m, g, "inconnu", 0.7)
    r.candidats.sort(key=lambda c: c.pos)

    meilleur = -1  # tranche indirecte : dernier indice rencontré
    for rx, t, conf in REGLES_TRANCHE:
        for m in rx.finditer(txt):
            if m.start() > meilleur:
                meilleur, r.tranche, r.tranche_conf, r.tranche_evidence = m.start(), t, conf, m.group(0)
    return r


def choisir_age(r: ResultatAge, demandeur: str | None) -> tuple[CandidatAge, float] | None:
    """Âge de la personne concernée : soi → âge de qui écrit ; proche/pro → âge du tiers ; le plus récent l'emporte."""
    if demandeur is None:
        cands = r.candidats
    elif demandeur == "soi":
        cands = [c for c in r.candidats if c.role in ("soi", "inconnu")]
    else:
        cands = [c for c in r.candidats if c.role in ("tiers", "inconnu")]
    if not cands:
        return None
    der = cands[-1]
    if demandeur is None and any(c.age != der.age for c in cands):
        return der, 0.5  # plusieurs âges, personne concernée incertaine : à confirmer
    return der, der.confidence


# ============================ DEMANDEUR ============================

METIERS = ("infirmiere scolaire|infirmier scolaire|infirmiere|infirmier|professeure|professeur|prof|enseignante|"
           "enseignant|cpe|as|assistante sociale|assistant social|educatrice|educateur|educ|medecin generaliste|"
           "medecin|generaliste|psychologue|aide-soignante|aide soignante|travailleuse sociale|travailleur social|"
           "conseillere|conseiller|animatrice|animateur|directrice|directeur|sage-femme|sage femme|policier|gendarme")
RE_METIER = re.compile(rf"\bje (?:suis|travaille comme|bosse comme)\s+(?:une |un )?({METIERS})\b")
RE_TIERS_PRO = re.compile(r"\b(?:un de mes patients|une de mes patientes|un de mes eleves|une de mes eleves|"
                          r"mon patient|ma patiente|une eleve|un eleve|un usager|une usagere|dans mon service|"
                          r"un beneficiaire|une beneficiaire|un resident|une residente|j'accompagne une|"
                          r"j'accompagne un)\b|\b(?:un|une)\s+(?:jeune|ado|personne|dame|monsieur|famille)"
                          r"(?:\s+de\s+\d{1,3}\s+ans)?\s+(?:que je suis|que j'accompagne|dont je m'occupe)\b")
RE_INQUIET = re.compile(rf"\b(?:inquiete?s? pour|je m'inquiete pour|on s'inquiete pour|aider|aide pour|souci avec|"
                        rf"probleme avec)\s+(?:(?:mon|ma|mes|notre|nos)\s+({RELATIONS})\b|un proche\b|une proche\b|quelqu'un\b)")
RE_RELATION = re.compile(rf"\b(?:mon|ma|mes|notre|nos)\s+({RELATIONS})\b")
RE_RESSENTI = re.compile(r"\b(?:je me sens|je suis|j'ai peur|je dors|je mange|je pleure|j'arrive|je n'arrive|"
                         r"j'en peux|je craque|je veux|je sais pas|j'ai envie|je me|moi je|j'ai)\b")
RE_PREMIERE = re.compile(r"\b(?:je|j'|moi|me|m')")
AVANT_NEUTRE = {"mort", "deces", "perte", "depuis", "avec", "chez", "comme"}
PREMIERE = {"je", "j'", "me", "m'", "moi", "on", "nous"}
DECES = {"mort", "morte", "decede", "decedee", "suicide", "suicidee", "tue", "tuee", "pendu", "pendue"}
TROISIEME = {"il", "elle", "ils", "elles", "ne", "n'", "se", "s'", "est", "va", "fait", "boit", "fume", "sort",
             "refuse", "pleure", "a", "parle", "veut", "dit", "crie", "mange", "dort", "reste", "passe", "frappe",
             "tape", "menace", "perd", "joue", "vit", "semble", "arrete", "traine", "fugue", "consomme", "prend"}


@dataclass
class ResultatDemandeur:
    valeur: str | None = None
    confidence: float = 0.0
    evidence: str = ""
    relation: str = ""


def extraire_demandeur(txt: str) -> ResultatDemandeur:
    R = ResultatDemandeur
    metier = RE_METIER.search(txt)
    tiers = RE_TIERS_PRO.search(txt)
    if tiers:
        return R("professionnel", 0.9 if metier else 0.8, tiers.group(0))
    m = RE_INQUIET.search(txt)
    if m:
        return R("proche", 0.9, m.group(0), m.group(1) or "")
    soi_relation = ""
    for m in RE_RELATION.finditer(txt):
        avant = tokens(txt[: m.start()])
        if avant and (avant[-1] in AVANT_NEUTRE or (len(avant) > 1 and avant[-1] == "de" and avant[-2] in AVANT_NEUTRE)):
            continue
        apres = tokens(txt[m.end():])
        i = 0  # sauter « de 15 ans », « 16 ans », « a 15 ans »
        while i < min(len(apres), 4) and (apres[i].isdigit() or apres[i] in ("de", "ans", "an", "aine", "ainee", "qui")
                                          or (apres[i] == "a" and i + 1 < len(apres) and apres[i + 1].isdigit())):
            i += 1
        if any(t in DECES for t in apres[i:i + 4]):
            continue
        for t in apres[i:i + 6]:
            if t in PREMIERE:
                soi_relation = soi_relation or f"{m.group(0)} {t}"
                break
            if t in TROISIEME:
                return R("proche", 0.9, m.group(0), m.group(1))
    if metier:
        return R("soi", 0.7, metier.group(0))  # « je suis infirmière et je suis épuisée »
    if soi_relation:
        return R("soi", 0.75, soi_relation)
    m = RE_RESSENTI.search(txt)
    if m:
        return R("soi", 0.7, m.group(0))
    m = RE_PREMIERE.search(txt)
    if m:
        return R("soi", 0.5, m.group(0))
    return R()


# ============================ CRISE (ne dépend jamais d'un LLM) ============================

NEGATEURS = {"pas", "jamais", "plus", "aucune", "aucun", "non", "sans", "ni", "rien"}
REPRISE = {"je", "j'", "veux", "vais", "mais", "et"}
RE_FIN_PROP = re.compile(r"[.;!?\n]|,\s|\bmais\b")
RE_RAPPORTE = re.compile(r"\b(?:s'est (?:suicide|suicidee|tue|tuee|pendu|pendue|jete|jetee)|a fait une tentative|"
                         r"quand j'avais|quand il avait|quand elle avait|il y a \d+ (?:ans|mois)|autrefois|"
                         r"a l'epoque|dans le passe|quand j'etais (?:ado|petit|petite|jeune))")


@dataclass
class Detection:
    niveau: int
    retenu: int
    extrait: str
    negation: bool = False
    rapporte: bool = False


@lru_cache
def _motifs_crise() -> list[tuple[int, re.Pattern]]:
    brut = yaml.safe_load((LEXIQUES / "crise.yaml").read_text(encoding="utf-8"))
    return [(n, re.compile(fold(p), re.IGNORECASE)) for n in (3, 2, 1) for p in brut.get(f"niveau_{n}", [])]


def _nie(txt: str, debut: int) -> bool:
    avant = txt[:debut]
    fins = list(RE_FIN_PROP.finditer(avant))
    if fins:
        avant = avant[fins[-1].end():]
    toks = tokens(avant)[-4:]
    for i in range(len(toks) - 1, -1, -1):
        t = toks[i]
        if t in REPRISE:  # « j'en peux plus je veux en finir » : pas une négation
            return t in ("je", "j'") and i > 0 and toks[i - 1] == "jamais"  # « jamais je me ferai du mal »
        if t in NEGATEURS:
            if t == "plus" and ((i > 0 and toks[i - 1] in ("de", "en")) or (i + 1 < len(toks) and toks[i + 1] == "en")):
                continue  # « de plus en plus »
            return True
    return False


def _rapporte(txt: str, debut: int, fin: int) -> bool:
    d = max(txt.rfind(c, 0, debut) for c in ".!?\n") + 1
    fs = [i for i in (txt.find(c, fin) for c in ".!?\n") if i >= 0]
    return bool(RE_RAPPORTE.search(txt[d: min(fs) if fs else len(txt)]))


def detecter_crise(txt: str) -> tuple[int, list[Detection], bool]:
    """Renvoie (niveau, détections, négation_détectée). Négation : -1 cran, jamais à 0 ; passé/rapporté : 1."""
    dets, pris = [], []
    for niveau, rx in _motifs_crise():
        for m in rx.finditer(txt):
            if any(m.start() < y and m.end() > x for x, y in pris):
                continue
            pris.append((m.start(), m.end()))
            d = Detection(niveau, niveau, m.group(0))
            if _nie(txt, m.start()):
                d.negation, d.retenu = True, max(niveau - 1, 1)
            if _rapporte(txt, m.start(), m.end()):
                d.rapporte, d.retenu = True, 1
            dets.append(d)
    niveau = max((d.retenu for d in dets), default=0)
    negation = any(d.negation and d.niveau > niveau for d in dets)
    return niveau, dets, negation


# ============================ LEXIQUE (parcours, statuts, type de demande, suivi) ============================

def _compiler(exprs: list[str]) -> list[re.Pattern]:
    out = []
    for e in exprs:
        f = fold(e)
        pat = re.escape(f).replace(r"\ ", r"\s+")
        if f[0].isalnum():
            pat = r"\b" + pat
        if f[-1].isalnum():
            pat += r"\b"
        out.append(re.compile(pat))
    return out


@lru_cache
def _lexique() -> dict:
    brut = yaml.safe_load((LEXIQUES / "parcours.yaml").read_text(encoding="utf-8"))
    return {
        "parcours": {k: _compiler(v) for k, v in brut["parcours"].items()},
        "statuts": {k: _compiler(v) for k, v in brut["statuts"].items()},
        "type_demande": {k: _compiler(v) for k, v in brut["type_demande"].items()},
        "suivi_oui": _compiler(brut["deja_suivi"]["oui"]),
        "suivi_non": _compiler(brut["deja_suivi"]["non"]),
    }


_NEG_LEX = {"pas", "aucun", "aucune", "sans", "jamais", "ni", "non"}


def _chercher(txt: str, motifs: list[re.Pattern]) -> list[str]:
    """Extraits trouvés, en ignorant ceux précédés d'une négation (« pas de violence »)."""
    out = []
    for rx in motifs:
        for m in rx.finditer(txt):
            avant = re.split(r"[.,;!?\n]", txt[: m.start()])[-1]
            if not set(tokens(avant)[-3:]) & _NEG_LEX:
                out.append(m.group(0))
                break
    return out


@dataclass
class ResultatLexique:
    parcours: list[tuple[str, list[str]]] = field(default_factory=list)  # trié par pertinence
    statuts: list[tuple[str, list[str]]] = field(default_factory=list)
    type_demande: list[tuple[str, list[str]]] = field(default_factory=list)
    deja_suivi: bool | None = None
    suivi_extrait: str = ""


def analyser_lexique(txt: str) -> ResultatLexique:
    lx, r = _lexique(), ResultatLexique()
    ordre = list(lx["parcours"])
    for p, motifs in lx["parcours"].items():
        if ex := _chercher(txt, motifs):
            r.parcours.append((p, ex))
    # nombre d'expressions trouvées ; « soins_ambulatoires » et « urgence » passent après les autres
    r.parcours.sort(key=lambda t: (t[0] in ("soins_ambulatoires", "urgence"), -len(t[1]), ordre.index(t[0])))
    for s, motifs in lx["statuts"].items():
        if ex := _chercher(txt, motifs):
            r.statuts.append((s, ex))
    for t, motifs in lx["type_demande"].items():
        if ex := _chercher(txt, motifs):
            r.type_demande.append((t, ex))
    if ex := _chercher(txt, lx["suivi_non"]):
        r.deja_suivi, r.suivi_extrait = False, ex[0]
    elif ex := _chercher(txt, lx["suivi_oui"]):
        r.deja_suivi, r.suivi_extrait = True, ex[0]
    return r
