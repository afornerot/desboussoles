"""Tests des extracteurs et de la fusion (situations fictives)."""
from datetime import date

import pytest

from app import extracteurs as ex
from app.fusion import construire
from app.gazetteer import gazetteer
from app.normalisation import texte

AUJ = date(2026, 10, 17)


@pytest.mark.parametrize("entree, attendu", [
    ("jsp quoi faire ma fille 16a", "je sais pas quoi faire ma fille 16a"),
    ("chui à bout, jpp", "je suis a bout, j'en peux plus"),
    ("ma mère a soixante-dix ans", "ma mere a 70 ans"),
    ("vingt et un ans", "21 ans"),
    ("une fille de dix-sept ans", "une fille de 17 ans"),
])
def test_normalisation(entree, attendu):
    assert texte(entree) == attendu


@pytest.mark.parametrize("message, demandeur, age", [
    ("j'ai 16 ans et je vais pas bien", None, 16),
    ("mon fils de 8 ans fait des cauchemars", "proche", 8),
    ("ma mère, 82 ans, perd la mémoire", "proche", 82),
    ("depuis 3 ans je dors mal", None, None),
    ("il a fait 2 ans de prison", None, None),
    ("j'ai 45 ans et mon fils de 15 ans va mal", "proche", 15),
    ("elle est née le 3/04/2011", None, 15),
    ("elle a 17 ans\nah non elle a 19 ans", "proche", 19),
])
def test_age(message, demandeur, age):
    choix = ex.choisir_age(ex.extraire_age(texte(message), AUJ), demandeur)
    assert (choix[0].age if choix else None) == age


@pytest.mark.parametrize("message, attendu", [
    ("mon fils ne sort plus de sa chambre", "proche"),
    ("mon fils me manque", "soi"),
    ("mon mari me frappe", "soi"),
    ("depuis la mort de mon père j'arrive plus à rien", "soi"),
    ("je suis infirmière scolaire, une élève de 4e m'inquiète", "professionnel"),
    ("je m'inquiète pour ma sœur", "proche"),
    ("je me sens pas bien", "soi"),
])
def test_demandeur(message, attendu):
    assert ex.extraire_demandeur(texte(message)).valeur == attendu


@pytest.mark.parametrize("message, niveau, negation", [
    ("je veux en finir", 2, False),
    ("j'ai pris tous mes cachets", 3, False),
    ("il me frappe encore", 3, False),
    ("je n'ai pas d'idées suicidaires", 1, True),
    ("pas d'idées noires hein, juste fatigué", 1, True),
    ("jamais je me ferai du mal", 1, True),
    ("j'en peux plus je veux en finir", 2, False),
    ("mon frère s'est suicidé il y a 5 ans", 1, False),
    ("je veux juste dormir et ne plus me réveiller", 2, False),
    ("jsp quoi faire ma fille 16a se scarifie", 2, False),
    ("je pense au sucide", 2, False),
    ("ça va", 0, False),
])
def test_crise(message, niveau, negation):
    n, _, neg = ex.detecter_crise(texte(message))
    assert (n, neg) == (niveau, negation)


@pytest.mark.parametrize("message, commune, conf_min", [
    ("on habite à Morteau", "Morteau", 1.0),
    ("je suis à l'hôpital depuis hier", None, 0),
    ("j'habite dans le Doubs", None, 0),
    ("on vit à Planoise", "Besançon", 0.9),
    ("je suis à Dijon", "Dijon", 0.8),
    ("je vis à besac", "Besançon", 1.0),
    ("j'habite à Morteu", "Morteau", 0.8),
])
def test_commune(message, commune, conf_min):
    r = gazetteer().extraire(texte(message))
    assert (r.nom or None) == commune
    if commune:
        assert r.confidence >= conf_min


def test_cp_partage_a_confirmer():
    r = gazetteer().extraire(texte("code postal 25300"))
    assert r.trouve and r.confidence == 0.4


def test_parcours_complet_et_reponses():
    p = construire("t", ["mon fils a 15 ans il sort plus de sa chambre et il fume des joints tous les jours"], aujourdhui=AUJ)
    assert p.demandeur.value == "proche" and p.personne.age_ans.value == 15
    assert p.personne.tranche_age.value == "13-17"
    assert p.champs_manquants == ["commune"]
    p = construire("t", ["mon fils a 15 ans il fume des joints", "on habite à Morteau"], aujourdhui=AUJ)
    assert p.personne.secteur_infanto_juvenile.value == "CGI Morteau" and p.pret


def test_besancon_mineur_quartier():
    p = construire("t", ["ma fille de 14 ans se scarifie, on habite à Besançon"], aujourdhui=AUJ)
    assert "quartier" in p.champs_manquants and p.risque.niveau == 2
    p = construire("t", ["ma fille de 14 ans se scarifie, on habite à Besançon"], {"quartier": "Battant"}, aujourdhui=AUJ)
    assert p.personne.secteur_infanto_juvenile.value == "CGI rue des Jardins Besançon"
    assert "quartier" not in p.champs_manquants


def test_besancon_adulte():
    p = construire("t", ["j'ai 30 ans j'habite à Besançon et je déprime"], aujourdhui=AUJ)
    assert p.personne.secteur_adulte.value == "BESANCON_A_PRECISER"


def test_reponse_utilisateur_prioritaire():
    p = construire("t", ["ma fille a 16 ans"], {"age": "17", "commune": "Ornans", "parcours": "ecoute_soutien"}, aujourdhui=AUJ)
    assert p.personne.age_ans.value == 17 and p.personne.age_ans.source.value == "utilisateur"
    assert p.personne.commune_nom.value == "Ornans"
    assert p.besoin.parcours_principal.value == "ecoute_soutien"
