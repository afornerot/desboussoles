"""Contrat avec le front : POST /sessions (vide) → question ; POST /reponse {session_id, id_question, reponse}
→ question suivante (avec le JSON patient temporaire), ou JSON patient final avec id_question = « fin »."""
import app.questions as questions_mod
from outils import client, envoyer, ids, parcourir


def test_post_sessions_vide_renvoie_la_premiere_question():
    r = client.post("/sessions")
    assert r.status_code == 201
    q = r.json()
    assert set(q) >= {"session_id", "id_question", "type", "texte", "mandatory", "options", "champ", "go_chat", "json_patient"}
    assert (q["id_question"], q["champ"], q["mandatory"], q["go_chat"]) == ("q_age", "age", True, False)
    assert "texte_tiers" not in q and "toujours" not in q  # configuration interne non envoyée
    assert q["json_patient"]["champs_manquants"] == ["age", "commune", "demandeur", "parcours"]


def test_ordre_complet_des_questions():
    vues, fin, _ = parcourir()
    assert ids(vues) == ["q_age", "q_adresse", "q_switch", "q_pour_qui", "q_duree", "q_impact", "q_impact_niveau",
                         "q_besoin", "q_accompagnement"]
    assert fin["id_question"] == "fin"
    b = fin["besoin"]
    assert (b["duree"]["value"], b["impact_domaine"]["value"], b["impact_niveau"]["value"], b["deja_suivi"]["value"]) == \
        ("moins_6_mois", "travail", "moderement", False)
    assert fin["personne"]["commune_nom"]["value"] == "Ornans" and fin["demandeur"]["value"] == "soi"
    assert b["parcours_principal"]["value"] == "ecoute_soutien"  # tiré du texte libre de q_besoin


def test_json_patient_temporaire_mis_a_jour():
    q = client.post("/sessions").json()
    q = envoyer(q["session_id"], "q_age", "15").json()
    assert q["id_question"] == "q_adresse" and q["json_patient"]["personne"]["age_ans"]["value"] == 15


def test_tutoiement_tiers_textes_adaptes():
    vues, _, _ = parcourir({"q_pour_qui": "tiers"})
    textes = {q["id_question"]: q["texte"] for q in vues}
    assert textes["q_besoin"].startswith("Quel est le principal besoin de la personne")
    assert textes["q_accompagnement"].startswith("La personne bénéficie-t-elle")
    vues, _, _ = parcourir({"q_pour_qui": "soi"})
    assert {q["id_question"]: q["texte"] for q in vues}["q_accompagnement"].startswith("Bénéficiez-vous")


def test_un_tiers_proche_ou_professionnel():
    _, fin, _ = parcourir({"q_pour_qui": "tiers", "q_besoin": "mon fils ne sort plus de sa chambre"})
    assert fin["demandeur"]["value"] == "proche"
    _, fin, _ = parcourir({"q_pour_qui": "tiers", "q_besoin": "je suis infirmière scolaire, une élève de 4e m'inquiète"})
    assert fin["demandeur"]["value"] == "professionnel"


def test_questions_facultatives_passees():
    pas = {k: "pass" for k in ("q_duree", "q_impact", "q_impact_niveau", "q_besoin", "q_accompagnement")}
    vues, fin, _ = parcourir(pas)
    assert fin["besoin"]["duree"]["value"] == "pass" and fin["besoin"]["deja_suivi"]["value"] == "pass"
    assert all(fin["suivi_questions"][k] == "pass" for k in pas)


def test_obligatoire_ne_peut_pas_etre_passee():
    sid = client.post("/sessions").json()["session_id"]
    for vide in ("", "pass", "PASS"):
        r = envoyer(sid, "q_age", vide)
        assert r.status_code == 422 and "obligatoire" in r.json()["error"]


def test_adresse_commune_extraite_sans_conserver_l_adresse():
    q = client.post("/sessions").json()
    sid = q["session_id"]
    envoyer(sid, "q_age", "40")
    q = envoyer(sid, "q_adresse", "3 rue de Morteau, 25000 Besançon").json()
    p = q["json_patient"]["personne"]
    assert p["commune_nom"]["value"] == "Besançon" and p["secteur_adulte"]["value"] == "BESANCON_A_PRECISER"
    assert "rue" not in str(q["json_patient"])  # l'adresse complète n'est pas conservée


def test_adresse_erreurs():
    sid = client.post("/sessions").json()["session_id"]
    envoyer(sid, "q_age", "40")
    r = envoyer(sid, "q_adresse", "12 rue des Lilas")
    assert r.status_code == 422 and "code postal" in r.json()["error"]
    r = envoyer(sid, "q_adresse", "1 rue X, 25300")
    assert r.status_code == 422 and "précisez" in r.json()["error"]
    q = envoyer(sid, "q_adresse", "8 avenue Foch, 21000 Dijon").json()
    assert q["json_patient"]["personne"]["hors_perimetre_guide"]["value"] is True


def test_mineur_besancon_quartier_demande():
    vues, fin, _ = parcourir({"q_age": "14", "q_adresse": "5 rue Battant, 25000 Besançon"})
    assert ids(vues)[2] == "q_quartier"
    assert fin["personne"]["secteur_infanto_juvenile"]["value"] == "CGI rue des Jardins Besançon"


def test_alerte_dans_le_texte_libre_fin_immediate():
    vues, fin, _ = parcourir({"q_besoin": "j'ai avalé toutes mes médocs"})
    assert ids(vues)[-1] == "q_besoin"  # q_accompagnement n'est pas posée
    assert fin["risque"]["niveau"] == 3 and fin["bloc_crise"]


def test_risque_2_bloc_crise_et_questionnaire_continue():
    sid = client.post("/sessions").json()["session_id"]
    for iq, rep in [("q_age", "34"), ("q_adresse", "25290 Ornans"), ("q_switch", "non"), ("q_pour_qui", "soi"),
                    ("q_duree", "pass"), ("q_impact", "pass"), ("q_impact_niveau", "pass")]:
        envoyer(sid, iq, rep)
    q = envoyer(sid, "q_besoin", "je veux en finir").json()
    assert q["id_question"] == "q_accompagnement" and q["bloc_crise"]


def test_pass_sur_un_champ_requis_rendu_facultatif(monkeypatch):
    qs = [q.model_copy(update={"mandatory": False}) if q.id_question == "q_age" else q for q in questions_mod.QUESTIONS]
    monkeypatch.setattr(questions_mod, "QUESTIONS", qs)
    q = client.post("/sessions").json()
    q = envoyer(q["session_id"], "q_age", "pass").json()
    assert q["id_question"] == "q_adresse" and q["json_patient"]["personne"]["age_ans"]["value"] == "pass"


def test_erreurs():
    assert envoyer("inconnue", "q_age", "x").status_code == 404
    sid = client.post("/sessions").json()["session_id"]
    assert envoyer(sid, "q_inexistante", "x").status_code == 422
    assert envoyer(sid, "q_age", "200").status_code == 422
    assert envoyer(sid, "q_age", "13-17").status_code == 422  # plus de tranche : un entier est attendu
    assert envoyer(sid, "q_age", "je sais pas").status_code == 422
    assert envoyer(sid, "q_duree", "un an").status_code == 422
    assert client.post("/reponse", json={"session_id": sid}).status_code == 422
    assert client.delete(f"/sessions/{sid}").status_code == 204


def test_catalogue():
    qs = client.get("/questions").json()
    assert qs[0]["id_question"] == "q_age" and qs[-1]["id_question"] == "q_accompagnement"
    assert len({q["id_question"] for q in qs}) == len(qs)
    assert {q["type"] for q in qs} == {"texte", "choice", "adresse", "textarea"}
    age = qs[0]
    assert (age["type"], age["options"]) == ("texte", [])


def test_age_texte_converti_en_entier():
    for saisie, attendu in (("12", 12), ("12 ans", 12), ("12ans", 12), (" 7 an ", 7), ("il a 12 ans", 12),
                            ("douze ans", 12), ("soixante-dix ans", 70), ("0", 0)):
        q = client.post("/sessions").json()
        valeur = envoyer(q["session_id"], "q_age", saisie).json()["json_patient"]["personne"]["age_ans"]["value"]
        assert valeur == attendu and type(valeur) is int, saisie  # entier dans le JSON, pas une chaîne


def test_age_ambigu_refuse():
    for saisie in ("entre 12 et 14 ans", "13-17", "quinze ou seize", "200 ans", "bientôt"):
        sid = client.post("/sessions").json()["session_id"]
        assert envoyer(sid, "q_age", saisie).status_code == 422, saisie
