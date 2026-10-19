"""Exporte les JSON Schema du contrat avec le front dans config/ :

  question.schema.json        question envoyée au front (réponse à POST /sessions et POST /reponse)
  reponse_front.schema.json   dictionnaire renvoyé par le front ({session_id, id_question, reponse})
  personne_finale.schema.json JSON patient final (id_question = « fin »)
  personne.schema.json        schéma patient seul (pour écrire des fiches à la main)

Lancer : .venv\\Scripts\\python.exe -m app.exporter_schemas
"""
from __future__ import annotations

import json
from pathlib import Path

from .main import PersonneFinale, QuestionEnvoyee, ReponseFront
from .schema import Personne

CONFIG = Path(__file__).resolve().parent.parent / "config"

SCHEMAS = {
    "question.schema.json": (QuestionEnvoyee, "Question envoyée au front"),
    "reponse_front.schema.json": (ReponseFront, "Réponse du front : {session_id, id_question, reponse}"),
    "personne_finale.schema.json": (PersonneFinale, "JSON patient final (id_question = « fin »), renvoyé si complet ou alerte"),
    "personne.schema.json": (Personne, "Schéma patient (Personne)"),
}


def exporter() -> list[Path]:
    ecrits = []
    for nom, (modele, titre) in SCHEMAS.items():
        s = modele.model_json_schema()
        s["title"] = titre
        s.setdefault("properties", {})["$schema"] = {"type": "string"}  # autorise "$schema" dans les fichiers d'exemple
        chemin = CONFIG / nom
        chemin.write_text(json.dumps(s, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        ecrits.append(chemin)
    return ecrits


if __name__ == "__main__":
    for c in exporter():
        print(f"écrit : {c}")
