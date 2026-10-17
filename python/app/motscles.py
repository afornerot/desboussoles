"""Mots-clés : les mots importants écrits par la personne (réponses textarea : début et fin du questionnaire),
qu'ils figurent ou non dans un lexique. Mots vides, nombres et mots de moins de 3 lettres exclus ;
abréviations SMS développées."""
from __future__ import annotations

import re

from .normalisation import _DIZAINES, _UNITES, fold, sms, tokens

_MOT = re.compile(r"[^\W_]+(?:['’\-][^\W_]+)*")
_ELISIONS = {"j", "l", "d", "m", "t", "s", "n", "c", "qu", "jusqu", "lorsqu", "puisqu"}

MOTS_VIDES = set("""
le la les un une des du de d au aux ce cet cette ces mon ma mes ton ta tes son sa ses notre nos votre vos leur leurs
je j tu il elle on nous vous ils elles me m te t se s moi toi lui eux y en
qui que qu quoi dont ou quand comment pourquoi combien quel quelle quels quelles
et mais donc or ni car si sinon comme alors puis ensuite aussi encore deja toujours jamais souvent parfois
ne n pas plus rien personne aucun aucune non oui ok bon bien tres trop peu beaucoup assez tout tous toute toutes
a dans sur sous avec sans pour par chez vers entre depuis pendant avant apres contre selon
ici cela ceci ca c celui celle ceux celles meme autre autres chaque quelque quelques certains
est es suis sommes etes sont etait etais etaient ete etre serai sera seront serait
ai as avons avez ont avait avais avaient eu avoir aura aurait
fait fais faire faut va vais vas vont aller peux peut pouvons pouvez peuvent pu veux veut voulons voulez veulent
sais sait savoir dit dis dire mis met prendre pris voir vois vu
habite habites habitons habitez habitent vit vis vivons vivez vivent
salut bonjour bonsoir merci desole hein euh bref voila ben bah enfin genre truc trucs chose choses
ans an jour jours fois mois semaine semaines heure heures moment temps
""".split()) | set(_UNITES) | set(_DIZAINES) | {"cent", "mille"}  # nombres en lettres


def extraire(texte: str) -> list[str]:
    """Mots-clés dans l'ordre d'apparition, sans doublon (comparaison sans accents ni casse)."""
    vus, out = set(), []

    def ajouter(mot: str) -> None:
        f = fold(mot)
        if len(f) < 3 or f in MOTS_VIDES or f in vus or f.isdigit():
            return
        vus.add(f)
        out.append(mot)

    table = sms()
    for m in _MOT.findall(texte.lower()):
        m = m.replace("’", "'")
        if "'" in m and fold(m[: m.rindex("'")]) in _ELISIONS:
            m = m[m.rindex("'") + 1:]
        if (dev := table.get(fold(m))) is not None:  # « jpp » → « j'en peux plus » → (mots vides)
            for w in dev.replace("'", " ").split():
                ajouter(w)
            continue
        ajouter(m)
    return out


def mots_de(extrait: str) -> list[str]:
    """Mots (sans accents) d'un extrait repéré par un extracteur, hors mots vides."""
    return [w for w in tokens(fold(extrait)) if w not in MOTS_VIDES]
