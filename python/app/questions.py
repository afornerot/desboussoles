"""Questions du parcours, lues dans config/questions.json (modifiable sans toucher au code ; autre fichier
possible via QUESTIONS_JSON). Le fichier est validé au démarrage. Régénérer le schéma d'édition :
python -m app.questions

Types : choice (boutons), texte (saisie courte), commune (commune ou code postal), adresse (adresse complète :
la commune en est extraite, l'adresse n'est pas conservée), textarea (texte libre ajouté à l'historique et analysé).

Quand une question est-elle posée ?
- question de champ « requis » (âge, commune, demandeur, quartier…) : seulement si le champ manque ;
- question marquée "toujours": true, textarea, ou question sans champ : jusqu'à ce qu'elle reçoive
  une réponse (ou soit passée, si elle est facultative).
Texte : "texte" (on s'adresse à la personne) ou "texte_tiers" (elle écrit pour quelqu'un d'autre), selon le demandeur.
"""
from __future__ import annotations

import json
import os
import re
from pathlib import Path
from typing import Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from .gazetteer import ResultatCommune, gazetteer
from .normalisation import texte as normaliser
from .schema import (DEMANDEURS, DUREES, IMPACT_DOMAINES, IMPACT_NIVEAUX, PARCOURS, TRANCHES_AGE, Option, Personne,
                     Question)

CONFIG = Path(__file__).resolve().parent.parent / "config"
# Champs qu'une question peut remplir
Champ = Literal["age", "commune", "demandeur", "parcours", "quartier", "duree", "impact_domaine", "impact_niveau",
                "deja_suivi"]
# Champs requis du schéma (calculer_manquants) qui doivent avoir une question ; « parcours » peut être couvert
# par une question textarea (texte libre analysé).
CHAMPS_REQUIS = ("age", "commune", "demandeur", "quartier")
TIERS = "tiers"  # réponse « un tiers » à la question demandeur : proche, ou professionnel si le texte l'indique
VALEURS = {
    "age": TRANCHES_AGE, "demandeur": DEMANDEURS + [TIERS], "parcours": [p for p in PARCOURS if p != "inconnu"],
    "duree": DUREES, "impact_domaine": IMPACT_DOMAINES, "impact_niveau": IMPACT_NIVEAUX, "deja_suivi": ["oui", "non"],
}


class OptionConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    valeur: str = Field(description="valeur enregistrée quand l'utilisateur clique sur l'option")
    libelle: str = Field(min_length=1, description="texte affiché sur le bouton")


class QuestionConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    id_question: str = Field(pattern=r"^[a-z0-9_]+$", description="identifiant unique (minuscules, chiffres, _)")
    type: Literal["choice", "texte", "commune", "adresse", "textarea"] = Field(
        description="choice : boutons · texte : saisie courte · commune : commune ou code postal · "
                    "adresse : adresse complète (la commune en est extraite) · textarea : texte libre analysé")
    champ: Optional[Champ] = Field(
        default=None, description="champ du schéma patient rempli par la réponse (absent pour textarea)")
    texte: str = Field(min_length=1, description="question posée en s'adressant à la personne (vouvoiement)")
    texte_tiers: Optional[str] = Field(
        default=None, min_length=1,
        description="variante quand la personne écrit pour quelqu'un d'autre (« La personne… ») ; défaut : texte")
    mandatory: bool = Field(description="true : réponse obligatoire ; false : la question peut être passée (« pass »)")
    toujours: bool = Field(default=False,
                           description="true : posée même si le champ est déjà rempli par le texte libre")
    options: list[OptionConfig] = Field(default_factory=list, description="boutons (type choice) ; quartier : vide = liste du gazetteer")

    @model_validator(mode="after")
    def coherence_type(self):
        q = f"question « {self.id_question} »"
        if self.type == "textarea":
            if self.champ or self.options:
                raise ValueError(f"{q} : une question textarea n'a ni champ ni options")
            return self
        if self.champ is None:  # question sans champ du schéma patient : choice ou texte
            if self.type in ("commune", "adresse"):
                raise ValueError(f"{q} : une question {self.type} remplit le champ commune")
            if self.type == "choice" and not self.options:
                raise ValueError(f"{q} : une question choice a besoin d'options")
            return self
        if (self.type in ("commune", "adresse")) != (self.champ == "commune"):
            raise ValueError(f"{q} : le champ commune utilise le type « commune » ou « adresse » (et eux seuls)")
        if self.type == "choice" and not self.options and self.champ != "quartier":
            raise ValueError(f"{q} : une question choice a besoin d'options")
        if self.type != "choice" and self.options:
            raise ValueError(f"{q} : seules les questions choice ont des options")
        return self

    @model_validator(mode="after")
    def valeurs_valides(self):
        if self.type == "textarea":
            return self
        permises = VALEURS.get(self.champ)
        for o in self.options:
            if permises is not None and o.valeur not in permises:
                raise ValueError(f"question « {self.id_question} » : option {o.valeur!r} invalide ; valeurs possibles : {permises}")
            if self.champ == "quartier" and not gazetteer().resoudre(o.valeur).quartier:
                raise ValueError(f"question « {self.id_question} » : quartier inconnu {o.valeur!r}")
        return self


class FichierQuestions(BaseModel):
    """Fichier de questions : l'ordre de la liste est l'ordre dans lequel les questions sont posées."""
    model_config = ConfigDict(extra="forbid", populate_by_name=True)
    schema_: Optional[str] = Field(default=None, alias="$schema")
    questions: list[QuestionConfig]

    @field_validator("questions")
    @classmethod
    def une_par_champ(cls, qs: list[QuestionConfig]):
        ids = [q.id_question for q in qs]
        if doublons := {i for i in ids if ids.count(i) > 1}:
            raise ValueError(f"id_question en double : {sorted(doublons)}")
        vus = [q.champ for q in qs if q.champ]
        if doublons := {c for c in vus if vus.count(c) > 1}:
            raise ValueError(f"une seule question par champ ; en double : {sorted(doublons)}")
        absents = [c for c in CHAMPS_REQUIS if c not in vus]
        if "parcours" not in vus and not any(q.type == "textarea" for q in qs):
            absents.append("parcours (question choice ou textarea)")
        if absents:
            raise ValueError(f"il manque une question pour : {absents} (ces champs ne pourraient jamais être remplis)")
        return qs


def charger(chemin: str | Path | None = None) -> list[Question]:
    """Lit et valide le fichier de questions ; lève ValueError avec un message clair s'il est invalide."""
    chemin = Path(chemin or os.environ.get("QUESTIONS_JSON") or CONFIG / "questions.json")
    try:
        brut = json.loads(chemin.read_text(encoding="utf-8"))
    except json.JSONDecodeError as e:
        raise ValueError(f"{chemin} : JSON invalide ligne {e.lineno}, colonne {e.colno} : {e.msg}") from e
    try:
        f = FichierQuestions.model_validate(brut)
    except Exception as e:
        raise ValueError(f"{chemin} : {e}") from e
    return [Question(id_question=q.id_question, type=q.type, champ=q.champ, texte=q.texte, mandatory=q.mandatory,
                     options=[Option(valeur=o.valeur, libelle=o.libelle) for o in q.options],
                     texte_tiers=q.texte_tiers, toujours=q.toujours) for q in f.questions]


QUESTIONS: list[Question] = charger()  # ordre = ordre du fichier


def question(id_question: str | None = None, champ: str | None = None) -> Question | None:
    for q in QUESTIONS:
        if (id_question and q.id_question == id_question) or (champ and not id_question and q.champ == champ):
            return q
    return None


def questions_pour(p: Personne, traitees: set[str] | None = None, avec_textarea: bool = True,
                   questions: list[Question] | None = None) -> list[Question]:
    """Questions restant à poser, dans l'ordre du fichier, avec le texte adapté au demandeur.
    avec_textarea=False (fiche écrite à la main) : seules les questions de champ."""
    traitees = traitees or set()
    manquants = set(p.champs_manquants)
    tiers = p.demandeur.sur and p.demandeur.value in ("proche", "professionnel")
    out = []
    for q in questions or QUESTIONS:
        if q.id_question in traitees:
            continue
        sans_champ = q.type == "textarea" or q.champ is None
        if not avec_textarea and (sans_champ or q.champ not in manquants):
            continue  # fiche écrite à la main : seulement les champs requis manquants
        if not (sans_champ or q.toujours or q.champ in manquants):
            continue
        q = q.model_copy(deep=True)
        if tiers and q.texte_tiers:
            q.texte = q.texte_tiers
        if q.champ == "quartier" and not q.options:
            q.options = [Option(valeur=x["nom"], libelle=x["nom"]) for x in gazetteer().quartiers]
        out.append(q)
    return out


_CP_VILLE = re.compile(r"\b(\d{5})\s+([^\d,;]+)")


def resoudre_adresse(adresse: str) -> ResultatCommune:
    """Commune d'une adresse complète : code postal et/ou nom de commune du Doubs (le dernier cité l'emporte,
    ex. « rue de Morteau, 25000 Besançon » → Besançon). Hors Doubs : « CP Ville » → hors périmètre."""
    gaz = gazetteer()
    r = gaz.extraire(normaliser(adresse))
    if r.trouve:
        return r
    if (m := _CP_VILLE.search(adresse)) and not m.group(1).startswith("25"):
        return ResultatCommune(hors_doubs=m.group(2).strip().title(), confidence=1.0, evidence=m.group(0))
    return gaz.resoudre(adresse)  # adresse réduite au nom de la commune (« Ornans »), éventuellement avec une faute


def valider_reponse(champ: str, valeur: str, type_question: str = "") -> str:
    """Contrôle une réponse et la renvoie normalisée (valeur enregistrée) ; lève ValueError si elle est invalide."""
    valeur = valeur.strip()
    if champ == "age":  # texte libre → entier : « 12 », « 12 ans », « 12ans », « il a 12 ans », « douze ans »
        nombres = re.findall(r"\d+", normaliser(valeur))  # normaliser : nombres en lettres → chiffres
        if len(nombres) == 1 and int(nombres[0]) <= 110:
            return str(int(nombres[0]))  # converti en int dans le JSON patient (fusion)
        raise ValueError("indiquez l'âge en années : un nombre entier entre 0 et 110")
    if champ in ("demandeur", "parcours", "duree", "impact_domaine", "impact_niveau", "deja_suivi"):
        if valeur.lower() in VALEURS[champ]:
            return valeur.lower()
        raise ValueError(f"valeurs possibles : {VALEURS[champ]}")
    if champ == "commune":
        gaz = gazetteer()
        if type_question == "adresse":
            r = resoudre_adresse(valeur)
            if not r.trouve:
                raise ValueError("commune introuvable dans l'adresse : ajoutez le code postal ou le nom de la commune")
            if r.confidence < 0.6:
                cps = re.findall(r"\b25\d{3}\b", valeur)
                noms = sorted(gaz.communes[i]["nom"] for cp in cps for i in gaz.par_cp.get(cp, []))
                raise ValueError(f"code postal partagé par plusieurs communes, précisez la commune : {', '.join(noms)}")
            return r.nom  # seule la commune est conservée, pas l'adresse
        r = gaz.resoudre(valeur)
        if not r.trouve:
            # commune hors du Doubs (le front propose toutes les communes de France) : acceptée,
            # elle sera marquée « hors périmètre du guide » (aucun secteur inventé)
            if len(valeur) < 2 or not any(ch.isalpha() for ch in valeur):
                raise ValueError(f"commune inconnue : {valeur!r}")
            return valeur
        if r.confidence < 0.6:
            noms = sorted(gaz.communes[i]["nom"] for i in gaz.par_cp.get(valeur, []))
            raise ValueError(f"code postal partagé par plusieurs communes, précisez : {', '.join(noms) or valeur}")
        return valeur
    if champ == "quartier":
        if not gazetteer().resoudre(valeur).quartier:
            raise ValueError(f"quartiers : {[q['nom'] for q in gazetteer().quartiers]}")
        return valeur
    raise ValueError(f"champ inconnu : {champ!r}")


if __name__ == "__main__":  # python -m app.questions : régénère config/questions.schema.json
    schema = FichierQuestions.model_json_schema(by_alias=True)
    schema["title"] = "Questions du parcours (l'ordre de la liste = l'ordre des questions)"
    (CONFIG / "questions.schema.json").write_text(json.dumps(schema, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"écrit : {CONFIG / 'questions.schema.json'}")
    charger()
    print("config/questions.json valide")
