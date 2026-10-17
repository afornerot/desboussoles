"""Fin du questionnaire : JSON patient final quand il n'y a plus de question, ou dès une alerte élevée."""
from fastapi.testclient import TestClient

from app.fusion import construire
from app.main import app

client = TestClient(app)


def test_cascade_complete_en_suivant_les_questions():
    q = client.post("/sessions").json()
    sid = q["session_id"]
    reponses = {"q_comment_ca_va": "je me sens pas bien depuis des semaines", "q_age": "34", "q_commune": "Ornans",
                "q_switch": "non", "q_demandeur": "soi", "q_parcours": "humeur_depression", "q_chose_a_rajouter": "pass"}
    vues = []
    while q["id_question"] != "fin":
        vues.append(q["id_question"])
        q = client.post("/reponse", json={"session_id": sid, "id_question": q["id_question"],
                                          "reponse": reponses[q["id_question"]]}).json()
    assert vues[0] == "q_comment_ca_va" and vues[-1] == "q_chose_a_rajouter"
    assert q["personne"]["commune_nom"]["value"] == "Ornans" and q["champs_manquants"] == []


def test_alerte_dans_une_chose_a_rajouter():
    sid = client.post("/sessions").json()["session_id"]
    client.post("/reponse", json={"session_id": sid, "id_question": "q_comment_ca_va", "reponse": "j'ai 40 ans, je vis à Ornans et je déprime"})
    fin = client.post("/reponse", json={"session_id": sid, "id_question": "q_chose_a_rajouter",
                                        "reponse": "il va me tuer, je suis en danger"}).json()
    assert fin["id_question"] == "fin" and fin["risque"]["niveau"] == 3


def test_pass_dans_le_schema_patient():
    p = construire("t", ["je me sens mal"], {"age": "pass", "commune": "pass", "parcours": "pass", "demandeur": "pass"})
    assert p.personne.age_ans.value == "pass" and p.personne.commune_nom.value == "pass"
    assert p.champs_manquants == []  # « pass » : renseigné, rien à redemander
    assert p.personne.tranche_age.value is None and p.personne.secteur_adulte.value is None  # pas de calcul sur « pass »


def test_fiche_manuelle_evaluee():
    fin = client.post("/personne/evaluer", json={"risque": {"niveau": 3, "indices": ["ce soir je le fais"]}}).json()
    assert fin["id_question"] == "fin"
