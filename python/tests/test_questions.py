"""Le fichier config/questions.json pilote l'ordre, les types, le caractère obligatoire et les textes."""
import json

import pytest

from app.fusion import construire
from app.questions import CONFIG, charger, questions_pour


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
    assert ids(qs) == ["q_comment_ca_va", "q_age", "q_commune", "q_switch", "q_demandeur", "q_parcours", "q_quartier", "q_chose_a_rajouter"]
    assert qs[0].type == qs[-1].type == "textarea" and qs[0].mandatory and not qs[-1].mandatory


def test_ordre_du_fichier_respecte(tmp_path):
    qs = base()
    qs.insert(1, qs.pop(2))  # commune avant âge
    perso = charger(ecrire(tmp_path, qs))
    p = construire("t", ["ça va pas"])
    assert ids(questions_pour(p, {"q_comment_ca_va"}, questions=perso))[:2] == ["q_commune", "q_age"]


def test_question_passee_n_est_plus_posee():
    p = construire("t", ["ça va pas"])
    assert "q_parcours" not in ids(questions_pour(p, {"q_comment_ca_va", "q_parcours"}))


@pytest.mark.parametrize("modif, message", [
    (lambda qs: qs.pop(1), "il manque une question"),
    (lambda qs: qs.append(dict(qs[1], id_question="q_autre")), "en double"),
    (lambda qs: qs.append(dict(qs[0])), "id_question en double"),
    (lambda qs: qs[4]["options"].append({"valeur": "voisin", "libelle": "x"}), "invalide"),
    (lambda qs: qs[1].pop("mandatory"), "mandatory"),
    (lambda qs: qs[1].update(type="liste"), "type"),
    (lambda qs: qs[2].update(type="texte"), "commune"),
    (lambda qs: qs[0].update(champ="age"), "textarea"),
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
