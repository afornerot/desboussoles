"""Fiches patient écrites à la main : validation par le schéma et évaluation par l'API."""
import json
from pathlib import Path

from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)
EXEMPLES = Path(__file__).resolve().parent.parent / "exemples"


def fiche(nom):
    return json.loads((EXEMPLES / nom).read_text(encoding="utf-8"))


def test_fiche_incomplete_mineur_besancon():
    fin = client.post("/personne/evaluer", json=fiche("patient_incomplet.json")).json()
    # plus de question sur le quartier : le JSON est renvoyé, le quartier reste dans les champs manquants
    assert fin["id_question"] == "fin" and fin["champs_manquants"] == ["quartier"]
    assert [s["id"] for s in fin["info_global"]] == ["urgences", "enfant", "proche"]


def test_fiche_complete():
    fin = client.post("/personne/evaluer", json=fiche("patient_complet.json")).json()
    assert fin["id_question"] == "fin" and fin["champs_manquants"] == []
    assert fin["personne"]["secteur_adulte"]["value"] == "PONTARLIER"
    assert fin["personne"]["tranche_age"]["value"] == "26-64"  # dérivé de l'âge


def test_fiche_vide_premiere_question_de_champ():
    assert client.post("/personne/evaluer", json={}).json()["id_question"] == "q_age"


def test_fiche_avec_pass():
    fin = client.post("/personne/evaluer", json={**fiche("patient_complet.json"),
                                                 "personne": {"age_ans": {"value": "pass", "confidence": 1},
                                                              "commune_nom": {"value": "Pontarlier", "confidence": 1}}}).json()
    assert fin["id_question"] == "fin" and fin["personne"]["age_ans"]["value"] == "pass"


def test_fiche_invalide_refusee():
    r = client.post("/personne/evaluer", json={"personne": {"age_ans": {"value": "quinze"}}})
    assert r.status_code == 422


def test_schema_exporte_a_jour():
    from app.schema import Personne
    fichier = json.loads((EXEMPLES.parent / "config" / "personne.schema.json").read_text(encoding="utf-8"))
    attendu = Personne.model_json_schema()
    assert fichier["properties"].keys() - {"$schema"} == attendu["properties"].keys(), \
        "schéma obsolète : lancer python -m app.exporter_schemas"
