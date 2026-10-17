"""Normalisation : minuscules, apostrophes, accents retirés, abréviations SMS, nombres en lettres."""
from __future__ import annotations

import re
import unicodedata
from functools import lru_cache
from pathlib import Path

import yaml

LEXIQUES = Path(__file__).resolve().parent.parent / "lexiques"

_REMPL = str.maketrans({"’": "'", "‘": "'", "`": "'", "´": "'", "«": '"', "»": '"', "“": '"', "”": '"',
                        " ": " ", " ": " ", "\t": " ", "\r": None, "﻿": None})


def fold(s: str) -> str:
    """Minuscules, apostrophes typographiques → ', accents supprimés."""
    s = s.lower().translate(_REMPL).replace("œ", "oe").replace("æ", "ae")
    s = unicodedata.normalize("NFD", s)
    return unicodedata.normalize("NFC", "".join(c for c in s if unicodedata.category(c) != "Mn"))


@lru_cache
def sms() -> dict[str, str]:
    brut = yaml.safe_load((LEXIQUES / "sms.yaml").read_text(encoding="utf-8"))
    return {fold(k): fold(v or "") for k, v in brut.items()}


_MOT = re.compile(r"[^\W_]+(?:'[^\W_]+)*")

_UNITES = {"zero": 0, "un": 1, "une": 1, "deux": 2, "trois": 3, "quatre": 4, "cinq": 5, "six": 6, "sept": 7,
           "huit": 8, "neuf": 9, "dix": 10, "onze": 11, "douze": 12, "treize": 13, "quatorze": 14, "quinze": 15,
           "seize": 16, "dix-sept": 17, "dix-huit": 18, "dix-neuf": 19}
_DIZAINES = {"vingt": 20, "vingts": 20, "trente": 30, "quarante": 40, "cinquante": 50, "soixante": 60}
_MOTS_NOMBRES = sorted(list(_UNITES) + list(_DIZAINES) + ["quatre-vingt", "quatre-vingts"], key=len, reverse=True)
_NW = "|".join(_MOTS_NOMBRES)
_NOMBRE = re.compile(rf"\b(?:{_NW})(?:(?:[- ]et[- ]|[- ])(?:{_NW}))*\b")


def _parse_nombre(m: str) -> int | None:
    parts = [p for p in m.replace(" ", "-").split("-") if p != "et"]
    toks, i = [], 0
    while i < len(parts):
        p = parts[i]
        if p == "dix" and i + 1 < len(parts) and f"dix-{parts[i + 1]}" in _UNITES:
            toks.append(f"dix-{parts[i + 1]}"); i += 2; continue
        if p == "quatre" and i + 1 < len(parts) and parts[i + 1] in ("vingt", "vingts"):
            toks.append("quatre-vingt"); i += 2; continue
        toks.append(p); i += 1
    total, i = 0, 0
    if toks and toks[0] == "quatre-vingt":
        total, i = 80, 1
    elif toks and toks[0] in _DIZAINES:
        total, i = _DIZAINES[toks[0]], 1
    if i < len(toks):
        u = _UNITES.get(toks[i])
        if u is None or (total and (u == 0 or (u >= 10 and total not in (60, 80)))):
            return None
        total += u
        i += 1
    return total if i == len(toks) and total <= 99 else None


def nombres_en_chiffres(s: str) -> str:
    def rep(m: re.Match) -> str:
        t = m.group(0)
        if t in ("un", "une"):  # articles
            return t
        v = _parse_nombre(t)
        return str(v) if v is not None else t
    return _NOMBRE.sub(rep, s)


def texte(s: str) -> str:
    """Texte normalisé pour les extracteurs (le texte original n'est pas modifié)."""
    s = fold(s)
    table = sms()
    s = _MOT.sub(lambda m: table.get(m.group(0), m.group(0)), s)
    s = nombres_en_chiffres(s)
    return re.sub(r" {2,}", " ", s).strip()


def tokens(s: str) -> list[str]:
    """Mots ; les apostrophes séparent (« j'ai » → « j' », « ai »)."""
    out = []
    for m in _MOT.findall(s):
        while "'" in m:
            i = m.index("'")
            out.append(m[: i + 1])
            m = m[i + 1:]
        if m:
            out.append(m)
    return out
