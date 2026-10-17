"""Contrat avec le front : POST /sessions (vide) → question ; POST /reponse {session_id, id_question, reponse}
→ question suivante, ou JSON patient final avec id_question = « fin »."""
import pytest
from fastapi.testclient import TestClient

import app.questions as questions_mod
from app.main import app

client = TestClient(app)


def demarrer():
    r = client.post("/sessions")
    assert r.status_code == 201
    return r.json()


def repondre(sid, id_question, reponse):
    return client.post("/reponse", json={"session_id": sid, "id_question": id_question, "reponse": reponse})


def test_post_sessions_vide_renvoie_comment_ca_va():
    q = demarrer()
    assert set(q) >= {"session_id", "id_question", "type", "texte", "mandatory", "options", "champ"}
    assert (q["id_question"], q["type"], q["texte"], q["mandatory"]) == ("q_comment_ca_va", "textarea", "Comment ça va ?", True)
    assert "personne" not in q and "bloc_crise" not in q


def test_cascade_jusqu_au_json_final():
    sid = demarrer()["session_id"]
    q = repondre(sid, "q_comment_ca_va", "mon fils a 15 ans il fume des joints tous les jours").json()
    assert q["id_question"] == "q_commune" and q["type"] == "commune"
    assert "personne" not in q and "demandeur" not in q  # le JSON patient n'est pas renvoyé à chaque tour

    r = repondre(sid, "q_commune", "25500")
    assert r.status_code == 422
    assert "précisez" in r.json()["detail"]["erreur"] and r.json()["detail"]["question"]["id_question"] == "q_commune"

    q = repondre(sid, "q_commune", "Morteau").json()
    assert q["id_question"] == "q_chose_a_rajouter" and q["mandatory"] is False

    fin = repondre(sid, "q_chose_a_rajouter", "pass").json()
    assert fin["id_question"] == "fin"
    assert fin["personne"]["secteur_infanto_juvenile"]["value"] == "CGI Morteau"
    assert fin["suivi_questions"] == {"q_comment_ca_va": "ok", "q_commune": "ok", "q_chose_a_rajouter": "pass"}
    assert "bloc_crise" not in fin


def test_obligatoire_ne_peut_pas_etre_passee():
    sid = demarrer()["session_id"]
    for vide in ("", "pass", "PASS"):
        r = repondre(sid, "q_comment_ca_va", vide)
        assert r.status_code == 422 and "obligatoire" in r.json()["detail"]["erreur"]


def test_pass_sur_un_champ_facultatif_ne_boucle_pas(monkeypatch):
    qs = [q.model_copy(update={"mandatory": False}) if q.id_question == "q_age" else q for q in questions_mod.QUESTIONS]
    monkeypatch.setattr(questions_mod, "QUESTIONS", qs)
    sid = demarrer()["session_id"]
    q = repondre(sid, "q_comment_ca_va", "je me sens pas bien, j'habite à Ornans").json()
    assert q["id_question"] == "q_age" and q["mandatory"] is False
    q = repondre(sid, "q_age", "pass").json()
    assert q["id_question"] != "q_age"  # pas reposée
    courant = client.get(f"/sessions/{sid}").json()
    assert courant["personne"]["age_ans"]["value"] == "pass" and "age" not in courant["champs_manquants"]


def test_alerte_renvoie_le_json_final_immediatement():
    sid = demarrer()["session_id"]
    fin = repondre(sid, "q_comment_ca_va", "j'ai avalé toutes mes médocs").json()
    assert fin["id_question"] == "fin" and fin["risque"]["niveau"] == 3
    assert fin["bloc_crise"] and "age" in fin["champs_manquants"]


def test_risque_2_question_suivante_avec_bloc_crise():
    sid = demarrer()["session_id"]
    q = repondre(sid, "q_comment_ca_va", "je veux en finir").json()
    assert q["id_question"] != "fin" and q["bloc_crise"]


def test_erreurs():
    assert repondre("inconnue", "q_comment_ca_va", "x").status_code == 404
    sid = demarrer()["session_id"]
    assert repondre(sid, "q_inexistante", "x").status_code == 422
    assert repondre(sid, "q_age", "200").status_code == 422
    assert client.post("/reponse", json={"session_id": sid}).status_code == 422  # clés manquantes
    assert client.delete(f"/sessions/{sid}").status_code == 204


def test_catalogue():
    qs = client.get("/questions").json()
    assert qs[0]["id_question"] == "q_comment_ca_va" and qs[-1]["texte"] == "Une chose à rajouter ?"
    assert len({q["id_question"] for q in qs}) == len(qs)
