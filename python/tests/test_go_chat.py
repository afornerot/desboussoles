"""go_chat (schéma question) : false par défaut, true dans les questions suivantes si la réponse à q_switch est « oui »."""
from outils import client, envoyer, ids, parcourir


def test_false_jusqu_a_q_switch_puis_true_si_oui():
    vues, fin, _ = parcourir({"q_switch": "oui"})
    i = ids(vues).index("q_switch")
    assert all(q["go_chat"] is False for q in vues[: i + 1])
    assert all(q["go_chat"] is True for q in vues[i + 1:]) and fin["go_chat"] is True  # le questionnaire continue


def test_non_reste_false():
    vues, fin, _ = parcourir({"q_switch": "non"})
    assert not any(q["go_chat"] for q in vues) and fin["go_chat"] is False


def test_q_switch_sans_champ():
    q = next(q for q in client.get("/questions").json() if q["id_question"] == "q_switch")
    assert q["champ"] is None and {o["valeur"] for o in q["options"]} == {"oui", "non"}


def test_valeur_invalide_refusee():
    sid = client.post("/sessions").json()["session_id"]
    r = envoyer(sid, "q_switch", "peut-être")
    assert r.status_code == 422 and r.json()["detail"]["question"]["id_question"] == "q_switch"


def test_le_front_n_envoie_pas_go_chat():
    sid = client.post("/sessions").json()["session_id"]
    r = client.post("/reponse", json={"session_id": sid, "id_question": "q_age", "reponse": "30", "go_chat": "oui"})
    assert r.json()["go_chat"] is False  # clé ignorée : seul q_switch compte
