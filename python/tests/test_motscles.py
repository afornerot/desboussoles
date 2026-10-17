"""Mots-clés tirés des réponses libres (« Comment ça va ? » et « Une chose à rajouter ? »)."""
import pytest
from fastapi.testclient import TestClient

from app import motscles
from app.fusion import construire
from app.main import app

client = TestClient(app)


@pytest.mark.parametrize("texte, attendu", [
    ("mon fils a 15 ans il sort plus de sa chambre et il fume des joints", ["fils", "sort", "chambre", "fume", "joints"]),
    ("Je me sens nulle, j'arrive plus à me lever le matin", ["sens", "nulle", "arrive", "lever", "matin"]),
    ("J’ai peur de l’hôpital, ma grand-mère y est morte", ["peur", "hôpital", "grand-mère", "morte"]),
    ("jsp jpp", []),  # abréviations développées en mots vides
    ("Fils, fils et FILS", ["fils"]),
])
def test_extraction(texte, attendu):
    assert motscles.extraire(texte) == attendu


def test_mots_hors_lexique_et_reperes():
    p = construire("t", ["mon fils fume des joints et joue à la console toute la nuit, on habite à Morteau"],
                   origines=["q_comment_ca_va"])
    mc = {m.mot: m for m in p.mots_cles}
    assert "console" in mc and mc["console"].reperes == []  # hors de tout lexique, gardé quand même
    assert "parcours:addiction" in mc["joints"].reperes
    assert "commune" in mc["morteau"].reperes
    assert "demandeur" in mc["fils"].reperes
    assert all(m.origine == "q_comment_ca_va" for m in p.mots_cles)


def test_crise_reperee():
    p = construire("t", ["je veux en finir"], origines=["q_comment_ca_va"])
    assert "crise:2" in {r for m in p.mots_cles for r in m.reperes}


def test_mots_cles_debut_et_fin_dans_le_json_final():
    sid = client.post("/sessions").json()["session_id"]
    envoi = lambda iq, r: client.post("/reponse", json={"session_id": sid, "id_question": iq, "reponse": r}).json()
    envoi("q_comment_ca_va", "j'ai 40 ans, je vis à Ornans et je déprime")
    envoi("q_switch", "non")
    fin = envoi("q_chose_a_rajouter", "mon chien est mort la semaine dernière")
    assert fin["id_question"] == "fin"
    origine = {m["mot"]: m["origine"] for m in fin["mots_cles"]}
    assert origine["déprime"] == "q_comment_ca_va" and origine["chien"] == "q_chose_a_rajouter"
