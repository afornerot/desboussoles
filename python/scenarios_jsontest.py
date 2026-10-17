"""Joue 5 parcours (fictifs) du questionnaire (config/questions.json) jusqu'au JSON patient final ;
enregistre chaque parcours dans jsontest/ (questions envoyées, réponses du front, JSON final).

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
        "description": "Un parent écrit pour son fils de 15 ans (tiers) ; questions à la troisième personne ; durée et impact renseignés.",
        "reponses": {
            "q_age": ["15"],
            "q_adresse": ["4 rue du Bief, 25500 Morteau"],
            "q_switch": ["non"],
            "q_pour_qui": ["tiers"],
            "q_duree": ["moins_6_mois"],
            "q_impact": ["etudes"],
            "q_impact_niveau": ["beaucoup"],
            "q_besoin": ["mon fils sort plus de sa chambre et il fume des joints tous les jours, je sais plus quoi faire"],
            "q_accompagnement": ["non"],
        },
    },
    {
        "fichier": "2_adulte_choisit_le_chat.json",
        "description": "Adulte qui écrit pour elle-même ; choisit le chatbot (q_switch = oui → go_chat = true) ; passe plusieurs questions facultatives.",
        "reponses": {
            "q_age": ["41"],
            "q_adresse": ["Ornans"],
            "q_switch": ["oui"],
            "q_pour_qui": ["soi"],
            "q_duree": ["pass"],
            "q_impact": ["travail"],
            "q_impact_niveau": ["pass"],
            "q_besoin": ["slt ca va pas du tout, chui crevé, j'ai perdu mon boulot et j'ai des dettes"],
            "q_accompagnement": ["pass"],
        },
    },
    {
        "fichier": "3_mineure_besancon_quartier.json",
        "description": "Mineure à Besançon : le quartier est demandé pour trouver le CGI de secteur ; déjà suivie.",
        "reponses": {
            "q_age": ["13"],
            "q_adresse": ["12 rue de Vesoul, 25000 Besançon"],
            "q_quartier": ["Planoise"],
            "q_switch": ["non"],
            "q_pour_qui": ["tiers"],
            "q_duree": ["moins_1_mois"],
            "q_impact": ["famille"],
            "q_impact_niveau": ["moderement"],
            "q_besoin": ["ma fille ne mange presque plus et pleure tous les soirs"],
            "q_accompagnement": ["oui"],
        },
    },
    {
        "fichier": "4_alerte_dans_le_texte_libre.json",
        "description": "Termes d'alerte élevée dans la réponse libre (q_besoin) : JSON renvoyé immédiatement, q_accompagnement n'est pas posée.",
        "reponses": {
            "q_age": ["27"],
            "q_adresse": ["9 grande rue, 25300 Pontarlier"],
            "q_switch": ["non"],
            "q_pour_qui": ["soi"],
            "q_duree": ["plus_6_mois"],
            "q_impact": ["relations_sociales"],
            "q_impact_niveau": ["beaucoup"],
            "q_besoin": ["j'en peux plus, ce soir je le fais, j'ai préparé ma lettre"],
        },
    },
    {
        "fichier": "5_professionnel_adresse_imprecise.json",
        "description": "Infirmière scolaire (tiers → professionnel) ; adresse sans commune refusée, puis code postal partagé refusé, puis adresse complète.",
        "reponses": {
            "q_age": ["14"],
            "q_adresse": ["12 rue des Lilas", "rue des Lilas, 25300", "rue des Lilas, 25300 Pontarlier"],
            "q_switch": ["non"],
            "q_pour_qui": ["tiers"],
            "q_duree": ["moins_15_jours"],
            "q_impact": ["etudes"],
            "q_impact_niveau": ["beaucoup"],
            "q_besoin": ["je suis infirmière scolaire, une élève me dit qu'elle se fait harceler et qu'elle veut disparaître pour toujours"],
            "q_accompagnement": ["pass"],
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
