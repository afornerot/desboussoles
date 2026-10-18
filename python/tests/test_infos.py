"""Section info_global : urgences toujours ; ressources enfant si mineur ; ressources proches si tiers."""
import json

import pytest

from app.infos import CONFIG, charger
from outils import client, envoyer, parcourir


def sections(payload):
    return {s["id"]: s for s in payload["info_global"]}


def test_urgences_des_la_premiere_question():
    q = client.post("/sessions").json()
    assert list(sections(q)) == ["urgences"]
    urg = sections(q)["urgences"]
    assert urg["titre"] == "Urgences" and "3114 (prévention du suicide)" in urg["elements"]
    assert set(urg) == {"id", "titre", "elements"}  # la condition n'est pas envoyée au front


def test_ressources_enfant_apres_l_age_si_mineur():
    q = client.post("/sessions").json()
    q = envoyer(q["session_id"], "q_age", "12 ans").json()
    assert list(sections(q)) == ["urgences", "enfant"]
    assert sections(q)["enfant"]["elements"] == ["CMPP", "PCO", "CMP Enfants", "CGI"]


def test_pas_de_ressources_enfant_si_majeur():
    q = client.post("/sessions").json()
    q = envoyer(q["session_id"], "q_age", "34").json()
    assert list(sections(q)) == ["urgences"]


def test_ressources_proches_apres_pour_qui_tiers():
    sid = client.post("/sessions").json()["session_id"]
    envoyer(sid, "q_age", "40")
    q = envoyer(sid, "q_adresse", "25290 Ornans").json()
    assert "proche" not in sections(q)
    q = envoyer(sid, "q_pour_qui", "tiers").json()
    assert list(sections(q)) == ["urgences", "proche"]
    assert "UNAFAM" in sections(q)["proche"]["elements"]


def test_mineur_et_tiers_cumules_jusqu_au_json_final():
    _, fin, _ = parcourir({"q_age": "15", "q_pour_qui": "tiers"})
    assert list(sections(fin)) == ["urgences", "enfant", "proche"]


def test_pour_moi_pas_de_ressources_proches():
    _, fin, _ = parcourir({"q_pour_qui": "soi"})
    assert "proche" not in sections(fin)


def test_fichier_infos_valide_et_erreurs(tmp_path):
    assert [s.id for s in charger()] == ["urgences", "enfant", "proche"]
    brut = json.loads((CONFIG / "infos.json").read_text(encoding="utf-8"))
    for modif, message in [
        (lambda b: b["sections"][1].update(condition="adulte"), "condition"),
        (lambda b: b["sections"].append(dict(b["sections"][0])), "en double"),
        (lambda b: b["sections"][0].update(elements=[]), "elements"),
    ]:
        b = json.loads(json.dumps(brut))
        modif(b)
        f = tmp_path / "infos.json"
        f.write_text(json.dumps(b, ensure_ascii=False), encoding="utf-8")
        with pytest.raises(ValueError, match=message):
            charger(f)
