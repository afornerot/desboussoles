"""Compatibilité avec le front DesBoussoles (app/internal/web/templates/guide.html du dépôt afornerot/desboussoles)."""
from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)


def test_post_sessions_avec_corps_vide_json():
    r = client.post("/sessions", content="{}", headers={"Content-Type": "application/json"})  # le front envoie '{}'
    assert r.is_success and r.json()["id_question"] == "q_comment_ca_va"


def test_bouton_passer_envoie_passe():
    sid = client.post("/sessions").json()["session_id"]
    client.post("/reponse", json={"session_id": sid, "id_question": "q_comment_ca_va", "reponse": "j'ai 30 ans, je vis à Ornans et je déprime"})
    client.post("/reponse", json={"session_id": sid, "id_question": "q_switch", "reponse": "non"})
    fin = client.post("/reponse", json={"session_id": sid, "id_question": "q_chose_a_rajouter", "reponse": "passe"}).json()
    assert fin["id_question"] == "fin" and fin["suivi_questions"]["q_chose_a_rajouter"] == "pass"
    assert not any(m["mot"] == "passe" for m in fin["mots_cles"])  # pas pris pour du texte libre


def test_erreur_lisible_dans_error():
    sid = client.post("/sessions").json()["session_id"]
    r = client.post("/reponse", json={"session_id": sid, "id_question": "q_comment_ca_va", "reponse": "passe"})
    assert r.status_code == 422 and "obligatoire" in r.json()["error"]
    assert r.json()["detail"]["question"]["id_question"] == "q_comment_ca_va"
    assert "error" in client.post("/reponse", json={"session_id": "x", "id_question": "q_age", "reponse": "1"}).json()


def test_commune_hors_doubs_acceptee():
    sid = client.post("/sessions").json()["session_id"]
    client.post("/reponse", json={"session_id": sid, "id_question": "q_comment_ca_va", "reponse": "j'ai 30 ans et je déprime"})
    q = client.post("/reponse", json={"session_id": sid, "id_question": "q_commune", "reponse": "Strasbourg"}).json()
    assert q["id_question"] != "q_commune"  # pas de blocage
    p = client.get(f"/sessions/{sid}").json()["personne"]
    assert p["commune_nom"]["value"] == "Strasbourg" and p["hors_perimetre_guide"]["value"] is True
    assert p["secteur_adulte"]["value"] is None  # aucun secteur inventé
