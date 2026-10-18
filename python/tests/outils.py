"""Aide aux tests : suivre le questionnaire question par question, comme le ferait le front."""
from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)

# Réponses par défaut (situation fictive), surchargées au besoin par chaque test
DEFAUT = {
    "q_age": "34", "q_adresse": "12 rue des Granges, 25290 Ornans", "q_quartier": "Battant",
    "q_pour_qui": "soi", "q_duree": "moins_6_mois", "q_impact": "travail", "q_impact_niveau": "moderement",
    "q_besoin": "je me sens seule et j'ai besoin de parler", "q_accompagnement": "non",
}


def envoyer(sid, id_question, reponse):
    return client.post("/reponse", json={"session_id": sid, "id_question": id_question, "reponse": reponse})


def parcourir(reponses=None, max_etapes=20):
    """Démarre une session et répond à chaque question jusqu'à « fin ».
    Renvoie (questions_vues, json_final, session_id)."""
    rep = {**DEFAUT, **(reponses or {})}
    q = client.post("/sessions").json()
    sid, vues = q["session_id"], []
    for _ in range(max_etapes):
        if q["id_question"] == "fin":
            return vues, q, sid
        vues.append(q)
        q = envoyer(sid, q["id_question"], rep[q["id_question"]]).json()
    raise AssertionError(f"pas de fin après {max_etapes} étapes : {[x['id_question'] for x in vues]}")


def ids(questions):
    return [q["id_question"] for q in questions]
