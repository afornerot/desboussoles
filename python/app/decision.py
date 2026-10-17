"""Décision de fin de questionnaire : quand renvoyer le JSON patient.

Évaluée après chaque réponse :
  1. alerte   : risque ≥ SEUIL_ALERTE (termes d'alerte élevée) → on arrête les questions, JSON renvoyé tout de suite ;
  2. complet  : plus aucune question à poser (champs remplis, questions facultatives passées,
                « Une chose à rajouter ? » traitée) → JSON renvoyé ;
  3. en_cours : sinon → on pose la question suivante.
L'alerte passe AVANT la complétude : on n'attend jamais d'avoir l'âge ou la commune pour remonter un danger.
"""
from __future__ import annotations

import os
from enum import Enum

from .schema import Personne, Question

# 3 = plan, moyen, imminence, danger immédiat (« j'ai pris tous mes cachets », « il va me tuer »…).
# Mettre SEUIL_ALERTE=2 pour arrêter aussi sur les idées suicidaires / auto-agression.
SEUIL_ALERTE = int(os.environ.get("SEUIL_ALERTE", "3"))


class Statut(str, Enum):
    en_cours = "en_cours"
    complet = "complet"
    alerte = "alerte"


def decider(p: Personne, restantes: list[Question]) -> tuple[Statut, str]:
    if p.risque.niveau >= SEUIL_ALERTE:
        indices = " », « ".join(p.risque.indices[:3])
        return Statut.alerte, f"alerte de niveau {p.risque.niveau} : « {indices} » — questionnaire interrompu"
    if not restantes:
        if p.champs_manquants:
            return Statut.complet, f"questionnaire terminé ; non renseigné (questions facultatives passées) : {', '.join(p.champs_manquants)}"
        return Statut.complet, "questionnaire terminé, tous les champs requis sont remplis"
    return Statut.en_cours, f"questions restantes : {', '.join(q.id_question for q in restantes)}"
