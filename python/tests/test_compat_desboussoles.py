"""Compatibilité avec le front DesBoussoles (app/internal/web/templates/guide.html du dépôt afornerot/desboussoles)."""
from outils import client, envoyer, parcourir


def test_post_sessions_avec_corps_vide_json():
    r = client.post("/sessions", content="{}", headers={"Content-Type": "application/json"})  # le front envoie '{}'
    assert r.is_success and r.json()["id_question"] == "q_age"


def test_bouton_passer_envoie_passe():
    _, fin, _ = parcourir({"q_besoin": "passe", "q_accompagnement": "passe"})
    assert fin["suivi_questions"]["q_besoin"] == "pass" and fin["besoin"]["deja_suivi"]["value"] == "pass"
    assert not any(m["mot"] == "passe" for m in fin["mots_cles"])  # pas pris pour du texte libre


def test_erreur_lisible_dans_error():
    sid = client.post("/sessions").json()["session_id"]
    r = envoyer(sid, "q_age", "passe")
    assert r.status_code == 422 and "obligatoire" in r.json()["error"]
    assert r.json()["detail"]["question"]["id_question"] == "q_age"
    assert "error" in envoyer("x", "q_age", "1").json()


def test_type_adresse_affichable():
    # le front affiche un champ texte pour tout type autre que choice / commune / textarea : « adresse » reste utilisable
    q = next(q for q in client.get("/questions").json() if q["id_question"] == "q_adresse")
    assert q["type"] == "adresse" and q["options"] == []
