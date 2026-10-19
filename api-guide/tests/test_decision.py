"""Fin du questionnaire : JSON patient final quand il n'y a plus de question, ou dès une alerte élevée."""
from app.fusion import construire
from outils import client, parcourir


def test_complet_apres_la_derniere_question():
    vues, fin, sid = parcourir()
    assert vues[-1]["id_question"] == "q_accompagnement" and fin["id_question"] == "fin"
    assert fin["champs_manquants"] == []
    assert client.get(f"/sessions/{sid}").json()["personne"]["age_ans"]["value"] == 34


def test_alerte_violences_en_cours():
    _, fin, _ = parcourir({"q_besoin": "il va me tuer, je suis en danger"})
    assert fin["risque"]["niveau"] == 3


def test_pass_dans_le_schema_patient():
    p = construire("t", ["je me sens mal"], {"age": "pass", "commune": "pass", "parcours": "pass", "demandeur": "pass"})
    assert p.personne.age_ans.value == "pass" and p.personne.commune_nom.value == "pass"
    assert p.champs_manquants == []  # « pass » : renseigné, rien à redemander
    assert p.personne.tranche_age.value is None and p.personne.secteur_adulte.value is None


def test_nouveaux_champs_dans_le_schema():
    p = construire("t", [], {"duree": "plus_6_mois", "impact_domaine": "famille", "impact_niveau": "beaucoup", "deja_suivi": "oui"})
    b = p.besoin
    assert (b.duree.value, b.impact_domaine.value, b.impact_niveau.value, b.deja_suivi.value) == \
        ("plus_6_mois", "famille", "beaucoup", True)


def test_fiche_manuelle_evaluee():
    fin = client.post("/personne/evaluer", json={"risque": {"niveau": 3, "indices": ["ce soir je le fais"]}}).json()
    assert fin["id_question"] == "fin"
