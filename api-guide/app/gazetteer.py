"""Gazetteer des communes du Doubs (data/gazetteer.json, construit à partir du guide PTSM 25)
et extraction de la commune / du quartier de Besançon dans le texte."""
from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from difflib import SequenceMatcher
from functools import lru_cache
from pathlib import Path

from .normalisation import fold

DATA = Path(__file__).resolve().parent.parent / "data" / "gazetteer.json"
SECTEUR_BESANCON_ADULTE = "BESANCON_A_PRECISER"  # « Pour BESANCON, la sectorisation est en fonction des rues »

MARQUEURS_FORTS = {"a", "au", "aux", "sur", "habite", "habitons", "habitez", "habitent", "habites", "vit",
                   "vivons", "vivent", "vis", "domicilie", "domiciliee", "installe", "installee"}
MARQUEURS_PROCHES = ["pres de", "cote de", "loin de", "proche de", "autour de", "environs de", "vers", "coin de"]
MARQUEURS_STRUCTURES = ["ch de", "chu de", "hopital de", "clinique de", "cmp de", "cmpp de", "urgences de",
                        "centre hospitalier de", "cgi de", "csapa de", "travaille a", "lycee de", "college de"]
MOTS_COURANTS = {"by", "doubs", "saone", "rang", "villers", "hopital", "fourg", "fourgs", "flagey", "montfort", "vaux",
                 "cour", "roche", "bart", "dung", "chay", "fins", "goux", "mont", "nods", "orve", "pin", "levier",
                 "busy", "osse", "morre", "vaire", "lods", "la veze", "les gras", "les combes", "la grange", "le puy",
                 "les allies", "les fins", "glay", "thise"}
QUARTIERS_GENERIQUES = {"la butte", "battant", "quartier battant", "saint claude", "la boucle"}
HORS_DOUBS = ["Dijon", "Belfort", "Vesoul", "Lons-le-Saunier", "Dole", "Lyon", "Paris", "Strasbourg", "Mulhouse",
              "Colmar", "Nancy", "Metz", "Chalon-sur-Saône", "Mâcon", "Auxerre", "Gray", "Lure", "Héricourt", "Delle",
              "Champagnole", "Lausanne", "Genève", "Neuchâtel", "La Chaux-de-Fonds", "Le Locle", "Marseille",
              "Toulouse", "Lille", "Nantes", "Bordeaux", "Grenoble"]


def cle(s: str) -> str:
    """Nom de lieu normalisé : sans accents ni ponctuation, « St »/« Ste » développés."""
    s = re.sub(r"([a-zà-ÿ])([A-Z])", r"\1 \2", s)
    mots = re.sub(r"[^a-z0-9]+", " ", fold(s)).split()
    return " ".join({"st": "saint", "ste": "sainte"}.get(m, m) for m in mots)


def _risque(k: str) -> bool:
    mots = k.split()
    return (k in MOTS_COURANTS or (len(mots) == 1 and len(k) <= 4)
            or (len(mots) == 2 and mots[0] in ("le", "la", "les", "l") and len(mots[1]) <= 6))


@dataclass
class ResultatCommune:
    commune: dict | None = None
    quartier: dict | None = None
    hors_doubs: str = ""
    confidence: float = 0.0
    evidence: str = ""
    candidats: list[str] = field(default_factory=list)

    @property
    def trouve(self) -> bool:
        return self.commune is not None or bool(self.hors_doubs)

    @property
    def nom(self) -> str:
        return self.commune["nom"] if self.commune else self.hors_doubs

    @property
    def hors_perimetre(self) -> bool:
        return bool(self.hors_doubs) or bool(self.commune and self.commune.get("hors_perimetre"))


class Gazetteer:
    def __init__(self, data: dict):
        self.communes = data["communes"]
        self.quartiers = data["quartiers"]
        self.par_cle: dict[str, list[int]] = {}
        self.par_cp: dict[str, list[int]] = {}
        self.quartier_cle: dict[str, int] = {}
        self.hors = {cle(h): h for h in HORS_DOUBS}
        self.besancon = -1
        for i, c in enumerate(self.communes):
            for k in [c.get("nom_norm") or cle(c["nom"])] + [cle(a) for a in c.get("alias") or []]:
                if k and i not in self.par_cle.setdefault(k, []):
                    self.par_cle[k].append(i)
            for cp in c.get("codes_postaux") or []:
                self.par_cp.setdefault(cp, []).append(i)
            if c.get("nom_norm") == "besancon":
                self.besancon = i
        for i, q in enumerate(self.quartiers):
            for a in [q["nom"]] + (q.get("alias") or []):
                self.quartier_cle[cle(a)] = i
        self.max_n = max(len(k.split()) for k in list(self.par_cle) + list(self.quartier_cle) + list(self.hors))

    def par_nom(self, nom: str) -> dict | None:
        ids = self.par_cle.get(cle(nom))
        return self.communes[ids[0]] if ids else None

    def extraire(self, txt: str) -> ResultatCommune:
        toks = [(m.group(0) if m.group(0) not in ("st", "ste") else {"st": "saint", "ste": "sainte"}[m.group(0)],
                 m.start(), m.end()) for m in re.finditer(r"[a-z0-9]+", txt)]
        mots = [t[0] for t in toks]
        cands, dbg = [], []  # (conf, pos, commune_idx, quartier_idx, hors, evidence)
        besancon_cite = any(w in ("besancon", "besac") for w in mots)

        def precede(i: int, motifs: list[str]) -> bool:
            return any(i >= len(m.split()) and mots[i - len(m.split()):i] == m.split() for m in motifs)

        i = 0
        while i < len(toks):
            trouve = 0
            for n in range(min(self.max_n, len(toks) - i), 0, -1):
                k = " ".join(mots[i:i + n])
                ev = txt[toks[i][1]:toks[i + n - 1][2]]
                fort = i > 0 and mots[i - 1] in MARQUEURS_FORTS
                proche, structure = precede(i, MARQUEURS_PROCHES), precede(i, MARQUEURS_STRUCTURES)
                if k in self.quartier_cle:
                    if k in QUARTIERS_GENERIQUES and not besancon_cite:
                        continue
                    cands.append((1.0 if besancon_cite else 0.9, toks[i][1], self.besancon, self.quartier_cle[k], "", ev))
                    dbg.append(f"quartier « {ev} »"); trouve = n; break
                if k in self.par_cle:
                    if _risque(k) and not fort:
                        dbg.append(f"ignoré (mot courant sans marqueur de lieu) « {ev} »"); continue
                    conf = 0.5 if (structure or proche) else 1.0 if fort else 0.9
                    ids = self.par_cle[k]
                    if len(ids) > 1:
                        conf = min(conf, 0.4)
                    cands.append((conf, toks[i][1], ids[0], -1, "", ev))
                    dbg.append(f"{self.communes[ids[0]]['nom']} « {ev} »"); trouve = n; break
                if k in self.hors and (fort or proche) and not structure:
                    cands.append((0.8, toks[i][1], -1, -1, self.hors[k], ev))
                    dbg.append(f"hors Doubs « {ev} »"); trouve = n; break
            i += trouve or 1

        if not cands:  # faute de frappe juste après un marqueur de lieu fort (« j'habite à Morteu »)
            for i in range(1, len(toks)):
                if mots[i - 1] not in MARQUEURS_FORTS or mots[i] in MARQUEURS_FORTS or precede(i, MARQUEURS_STRUCTURES):
                    continue
                for n in range(min(3, len(toks) - i), 0, -1):
                    k = " ".join(mots[i:i + n])
                    if len(k) >= 5 and (idx := self._approx(k)) is not None:
                        cands.append((0.8, toks[i][1], idx, -1, "", txt[toks[i][1]:toks[i + n - 1][2]]))
                        dbg.append(f"≈ {self.communes[idx]['nom']}"); break

        for m in re.finditer(r"\b(25\d{3})\b", txt):  # codes postaux
            ids = self.par_cp.get(m.group(1), [])
            if len(ids) == 1:
                cands.append((0.95, m.start(), ids[0], -1, "", m.group(1)))
            elif ids:  # CP partagé : ne pas choisir (0.4, à confirmer), sauf si un nom cité correspond
                if any(c[2] in ids for c in cands):
                    cands = [(1.0, *c[1:]) if c[2] in ids else c for c in cands]
                else:
                    best = max(ids, key=lambda j: self.communes[j].get("population") or 0)
                    cands.append((0.4, m.start(), best, -1, "", m.group(1)))
                    dbg.append(f"CP {m.group(1)} partagé par {len(ids)} communes")

        if not cands:
            return ResultatCommune(candidats=dbg)
        conf, _, ci, qi, hors, ev = max(cands, key=lambda c: (c[0], c[1]))  # le plus sûr, puis le plus récent
        r = ResultatCommune(confidence=conf, evidence=ev, candidats=dbg, hors_doubs=hors)
        if ci >= 0:
            r.commune = self.communes[ci]
        if qi >= 0:
            r.quartier = self.quartiers[qi]
        elif ci == self.besancon and ci >= 0:
            for c in cands:
                if c[3] >= 0:
                    r.quartier = self.quartiers[c[3]]
        return r

    def resoudre(self, lieu: str) -> ResultatCommune:
        """Résout une réponse de l'utilisateur (nom de commune, code postal, quartier de Besançon)."""
        k = cle(lieu)
        if not k:
            return ResultatCommune()
        q = self.quartier_cle.get(k.removeprefix("besancon "))
        if q is not None and self.besancon >= 0:
            return ResultatCommune(commune=self.communes[self.besancon], quartier=self.quartiers[q], confidence=1.0, evidence=lieu)
        r = self.extraire("a " + k)
        if r.trouve:
            r.confidence = max(r.confidence, 0.95) if r.confidence > 0.4 else r.confidence
            return r
        if (idx := self._approx(k)) is not None:
            return ResultatCommune(commune=self.communes[idx], confidence=0.8, evidence=lieu)
        return ResultatCommune()

    def _approx(self, k: str) -> int | None:
        best, bi = 0.0, None
        for c, ids in self.par_cle.items():
            if abs(len(c) - len(k)) > 2 or _risque(c):
                continue
            s = SequenceMatcher(None, k, c).ratio()
            if s > best:
                best, bi = s, ids[0]
        return bi if best >= 0.88 else None


@lru_cache
def gazetteer() -> Gazetteer:
    return Gazetteer(json.loads(DATA.read_text(encoding="utf-8")))
