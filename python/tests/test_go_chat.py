"""go_chat (schéma question) : false par défaut, true dans les questions suivantes si la réponse à q_switch est « oui »."""
from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)


def envoyer(sid, id_question, reponse):
    return client.post("/reponse", json={"session_id": sid, "id_question": id_question, "reponse": reponse})


def test_q_switch_posee_apres_age_et_commune():
    q = client.post("/sessions").json()
    assert q["go_chat"] is False
    sid = q["session_id"]
    q = envoyer(sid, "q_comment_ca_va", "ça va pas").json()
    assert q["id_question"] == "q_age" and q["go_chat"] is False
    envoyer(sid, "q_age", "34")
    q = envoyer(sid, "q_commune", "Ornans").json()
    assert q["id_question"] == "q_switch" and q["type"] == "choice" and q["champ"] is None
    assert {o["valeur"] for o in q["options"]} == {"oui", "non"} and q["go_chat"] is False


def test_oui_go_chat_true_dans_les_questions_suivantes():
    sid = client.post("/sessions").json()["session_id"]
    q = envoyer(sid, "q_comment_ca_va", "j'ai 34 ans, je vis à Ornans et ça va pas").json()
    assert q["id_question"] == "q_switch"  # âge et commune déjà connus : q_switch arrive tout de suite
    q = envoyer(sid, "q_switch", "oui").json()
    assert q["id_question"] != "fin" and q["go_chat"] is True  # le questionnaire continue, le front décide
    q = envoyer(sid, q["id_question"], "soi" if q["id_question"] == "q_demandeur" else "ecoute_soutien").json()
    assert q["go_chat"] is True  # reste vrai


def test_non_go_chat_reste_false():
    sid = client.post("/sessions").json()["session_id"]
    envoyer(sid, "q_comment_ca_va", "ça va pas")
    assert envoyer(sid, "q_switch", "non").json()["go_chat"] is False


def test_valeur_invalide_refusee():
    sid = client.post("/sessions").json()["session_id"]
    envoyer(sid, "q_comment_ca_va", "ça va pas")
    r = envoyer(sid, "q_switch", "peut-être")
    assert r.status_code == 422 and r.json()["detail"]["question"]["id_question"] == "q_switch"


def test_le_front_n_envoie_pas_go_chat():
    sid = client.post("/sessions").json()["session_id"]
    r = client.post("/reponse", json={"session_id": sid, "id_question": "q_comment_ca_va", "reponse": "ça va", "go_chat": "oui"})
    assert r.json()["go_chat"] is False  # clé ignorée : seul q_switch compte
