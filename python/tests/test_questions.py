"""Le fichier config/questions.json pilote l'ordre, les types, les textes (direct / tiers) et le caractère obligatoire."""
import json

import pytest

from app.fusion import construire
from app.questions import CONFIG, charger, questions_pour, resoudre_adresse


def ecrire(tmp_path, questions):
    f = tmp_path / "questions.json"
    f.write_text(json.dumps({"questions": questions}, ensure_ascii=False), encoding="utf-8")
    return f


def base():
    return json.loads((CONFIG / "questions.json").read_text(encoding="utf-8"))["questions"]


def ids(qs):
    return [q.id_question for q in qs]


def test_fichier_livre_valide():
    qs = charger()
    assert ids(qs) == ["q_age", "q_adresse", "q_quartier", "q_pour_qui", "q_duree", "q_impact",
                       "q_impact_niveau", "q_besoin", "q_accompagnement"]
    toujours = {q.id_question for q in qs if q.toujours}
    assert toujours == {"q_duree", "q_impact", "q_impact_niveau", "q_accompagnement"}
    assert all(not q.mandatory for q in qs if q.id_question in toujours | {"q_besoin"})


def test_ordre_du_fichier_respecte(tmp_path):
    qs = base()
    qs.insert(0, qs.pop(1))  # adresse avant âge
    perso = charger(ecrire(tmp_path, qs))
    assert ids(questions_pour(construire("t", []), questions=perso))[:2] == ["q_adresse", "q_age"]


def test_texte_tiers_selon_le_demandeur():
    direct = {q.id_question: q.texte for q in questions_pour(construire("t", [], {"demandeur": "soi"}))}
    tiers = {q.id_question: q.texte for q in questions_pour(construire("t", [], {"demandeur": "tiers"}))}
    assert direct["q_accompagnement"].startswith("Bénéficiez-vous")
    assert tiers["q_accompagnement"].startswith("La personne bénéficie-t-elle")
    assert direct["q_duree"] == tiers["q_duree"]  # pas de variante tiers : même texte


def test_question_toujours_posee_meme_si_champ_rempli():
    p = construire("t", ["je suis suivie par une psychologue"])  # deja_suivi rempli par le texte
    assert p.besoin.deja_suivi.value is True
    assert "q_accompagnement" in ids(questions_pour(p))
    assert "q_accompagnement" not in ids(questions_pour(p, {"q_accompagnement"}))  # une fois répondue


@pytest.mark.parametrize("adresse, commune, hors", [
    ("12 rue des Granges, 25290 Ornans", "Ornans", False),
    ("3 rue de Morteau, 25000 Besançon", "Besançon", False),  # le CP et la commune finale l'emportent sur la rue
    ("5 place de l'Hôtel de Ville, Pontarlier", "Pontarlier", False),
    ("8 avenue Foch, 21000 Dijon", "Dijon", True),
    ("10 rue Nationale, 35000 Rennes", "Rennes", True),
])
def test_resoudre_adresse(adresse, commune, hors):
    r = resoudre_adresse(adresse)
    assert (r.nom, r.hors_perimetre) == (commune, hors)


@pytest.mark.parametrize("modif, message", [
    (lambda qs: qs.pop(1), "il manque une question"),
    (lambda qs: qs.append(dict(qs[1], id_question="q_autre")), "en double"),
    (lambda qs: qs.append(dict(qs[0])), "id_question en double"),
    (lambda qs: qs[3]["options"].append({"valeur": "voisin", "libelle": "x"}), "invalide"),
    (lambda qs: qs[4]["options"].append({"valeur": "un_an", "libelle": "x"}), "invalide"),
    (lambda qs: qs[1].pop("mandatory"), "mandatory"),
    (lambda qs: qs[1].update(type="liste"), "type"),
    (lambda qs: qs[1].update(type="texte"), "commune"),
    (lambda qs: qs[7].update(champ="age"), "textarea"),
    (lambda qs: qs[3].pop("options"), "options"),
    (lambda qs: qs[1].update(id_question="Q âge"), "id_question"),
])
def test_erreurs_claires(tmp_path, modif, message):
    qs = base()
    modif(qs)
    with pytest.raises(ValueError, match=message):
        charger(ecrire(tmp_path, qs))


def test_json_mal_forme(tmp_path):
    f = tmp_path / "q.json"
    f.write_text('{"questions": [ { "champ": "age", }', encoding="utf-8")
    with pytest.raises(ValueError, match="ligne 1"):
        charger(f)
