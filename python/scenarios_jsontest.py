"""Joue 5 parcours (fictifs) : « Comment ça va ? » (saisie libre) → questions sur les champs manquants →
« Une chose à rajouter ? » → JSON patient final ; enregistre chaque parcours dans jsontest/.

Lancer : .venv\\Scripts\\python.exe scenarios_jsontest.py
(pas besoin de démarrer le serveur : l'API est appelée en direct via TestClient)
"""
from __future__ import annotations

import json
from pathlib import Path

from fastapi.testclient import TestClient

from app.main import app

SORTIE = Path(__file__).resolve().parent / "jsontest"

# Réponses de « l'utilisateur » par id_question, dans l'ordre (une 2e valeur sert si la 1re est refusée).
# "pass" = question facultative laissée sans réponse.
SCENARIOS = [
    {
        "fichier": "1_proche_ado_addiction.json",
        "description": "Un parent écrit pour son fils de 15 ans ; seule la commune manque ; rien à ajouter.",
        "reponses": {
            "q_comment_ca_va": ["mon fils a 15 ans il sort plus de sa chambre et il fume des joints tous les jours, je sais plus quoi faire"],
            "q_commune": ["Morteau"],
            "q_switch": ["non"],
            "q_chose_a_rajouter": ["pass"],
        },
    },
    {
        "fichier": "2_adulte_message_vague.json",
        "description": "Message court et vague : âge, commune et besoin sont demandés ; la personne choisit le chat (q_switch = oui → go_chat = true) ; un complément est ajouté à la fin.",
        "reponses": {
            "q_comment_ca_va": ["slt ca va pas du tout en ce moment, chui crevé"],
            "q_age": ["41"],
            "q_commune": ["Ornans"],
            "q_switch": ["oui"],
            "q_demandeur": ["soi"],
            "q_parcours": ["ecoute_soutien"],
            "q_chose_a_rajouter": ["j'ai perdu mon boulot il y a deux mois et j'ai des dettes"],
        },
    },
    {
        "fichier": "3_mineure_besancon_quartier.json",
        "description": "Mineure à Besançon : le quartier est demandé pour trouver le CGI de secteur.",
        "reponses": {
            "q_comment_ca_va": ["ma fille de 13 ans ne mange presque plus et pleure tous les soirs, on habite à Besançon"],
            "q_switch": ["non"],
            "q_quartier": ["Planoise"],
            "q_parcours": ["specifique"],
            "q_chose_a_rajouter": ["elle est suivie par l'infirmière du collège"],
        },
    },
    {
        "fichier": "4_alerte_immediate.json",
        "description": "Termes d'alerte élevée dès « Comment ça va ? » : JSON renvoyé immédiatement, sans autre question.",
        "reponses": {"q_comment_ca_va": ["j'en peux plus, ce soir je le fais, j'ai préparé ma lettre"]},
    },
    {
        "fichier": "5_professionnel_cp_partage.json",
        "description": "Infirmière scolaire ; code postal partagé refusé puis précisé ; rien à ajouter.",
        "reponses": {
            "q_comment_ca_va": ["Bonjour, je suis infirmière scolaire, une élève de 4e me dit qu'elle se fait harceler et qu'elle veut disparaître pour toujours"],
            "q_age": ["13-17"],
            "q_commune": ["25300", "Pontarlier"],
            "q_switch": ["non"],
            "q_parcours": ["victimologie_vif"],
            "q_chose_a_rajouter": ["pass"],
        },
    },
]


def jouer(client: TestClient, sc: dict) -> dict:
    """Échanges exacts avec le front : question envoyée → dictionnaire {session_id, id_question, reponse} reçu."""
    reponses = {k: list(v) for k, v in sc["reponses"].items()}
    question = client.post("/sessions").json()  # POST vide
    sid = question["session_id"]
    trace = {"description": sc["description"], "echanges": []}

    for _ in range(20):  # garde-fou
        if question["id_question"] == "fin":
            break
        if not reponses.get(question["id_question"]):
            raise RuntimeError(f"{sc['fichier']} : pas de réponse prévue pour « {question['id_question']} »")
        envoi = {"session_id": sid, "id_question": question["id_question"], "reponse": reponses[question["id_question"]].pop(0)}
        r = client.post("/reponse", json=envoi)
        echange = {"question_envoyee_au_front": question, "reponse_du_front": envoi}
        if r.status_code != 200:
            echange["refus"] = r.json()["detail"]["erreur"]  # la même question est reposée
            question = r.json()["detail"]["question"]
        else:
            question = r.json()
        trace["echanges"].append(echange)

    trace["json_patient_final"] = question  # contient "id_question": "fin"
    return trace


def main() -> None:
    SORTIE.mkdir(exist_ok=True)
    client = TestClient(app)
    for sc in SCENARIOS:
        trace = jouer(client, sc)
        (SORTIE / sc["fichier"]).write_text(json.dumps(trace, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        etapes = " > ".join(f"{x['reponse_du_front']['id_question'].removeprefix('q_')}"
                            + (" (refusé)" if "refus" in x else "") + (" [pass]" if x["reponse_du_front"]["reponse"] == "pass" else "")
                            for x in trace["echanges"])
        fin = trace["json_patient_final"]
        nature = "alerte" if fin["risque"]["niveau"] >= 3 else "complet"
        print(f"{sc['fichier']:36} fin ({nature:7}) {etapes}")
    print(f"\n→ {len(SCENARIOS)} fichiers dans {SORTIE}")


if __name__ == "__main__":
    main()
