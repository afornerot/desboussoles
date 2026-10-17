"""Expressions ajoutées aux lexiques (dépression, écoute, neurodéveloppement, suivi, crise)."""
import pytest

from app import extracteurs as ex
from app.normalisation import texte


def parcours(message):
    return [p for p, _ in ex.analyser_lexique(texte(message)).parcours]


@pytest.mark.parametrize("message", [
    "j'ai des idées sombres", "je pense à mon enterrement", "j'ai du mal à rester propre", "aucune énergie",
    "je ne dors pas", "je mange n'importe quoi", "je dors constamment", "j'ai peu d'estime de moi",
    "j'ai du mal à respirer", "je suis traumatisé", "j'en ai marre", "je n'ai plus la force de me battre",
])
def test_depression(message):
    assert "humeur_depression" in parcours(message)


@pytest.mark.parametrize("message", ["j'ai besoin de crier", "je suis seule", "j'ai besoin d'écoute"])
def test_ecoute(message):
    assert "ecoute_soutien" in parcours(message)


@pytest.mark.parametrize("message", [
    "il a du mal à se faire des amis",
    "c'est difficile de comprendre les autres", "difficile d'être compris", "du mal à connecter avec les autres",
    "je me sens incompris", "j'ai l'impression de ne pas être comme les autres", "je suis bizarre",
])
def test_neurodeveloppement(message):
    assert "specifique" in parcours(message)


@pytest.mark.parametrize("message, attendu", [
    ("je dois renouveller mon traitement", True),
    ("il me faut une prescription", True),
    ("je suis suivi", True),
    ("pas de suivi pour l'instant", False),  # la négation l'emporte
])
def test_suivi(message, attendu):
    assert ex.analyser_lexique(texte(message)).deja_suivi is attendu


@pytest.mark.parametrize("message, niveau", [
    ("j'ai des idées sombres", 2),
    ("je pense à mon enterrement", 2),
    ("aucune envie d'être là", 2),
    ("je n'ai plus la force de me battre", 2),
    ("j'en ai marre", 1),
    ("aucune énergie", 1),
])
def test_crise(message, niveau):
    assert ex.detecter_crise(texte(message))[0] == niveau
