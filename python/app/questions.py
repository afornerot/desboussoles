"""Questions du parcours : textarea d'ouverture (« Comment ça va ? »), questions pour les champs manquants,
textarea de clôture (« Une chose à rajouter ? »).

Textes, types, options, caractère obligatoire et ORDRE viennent de config/questions.json (modifiable sans
toucher au code ; autre fichier possible via QUESTIONS_JSON). Le fichier est validé au démarrage.
Régénérer le schéma d'édition : python -m app.questions

Types : choice (boutons), texte (saisie courte), commune (commune ou code postal), textarea (texte libre :
la réponse est ajoutée à l'historique et ré-analysée comme une saisie libre).
"""
from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from .gazetteer import gazetteer
from .schema import DEMANDEURS, PARCOURS, TRANCHES_AGE, Option, Personne, Question

CONFIG = Path(__file__).resolve().parent.parent / "config"
CHAMPS = ("age", "commune", "demandeur", "parcours", "quartier")  # champs requis du schéma (calculer_manquants)


class OptionConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    valeur: str = Field(description="valeur enregistrée quand l'utilisateur clique sur l'option")
    libelle: str = Field(min_length=1, description="texte affiché sur le bouton")


class QuestionConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    id_question: str = Field(pattern=r"^[a-z0-9_]+$", description="identifiant unique (minuscules, chiffres, _)")
    type: Literal["choice", "texte", "commune", "textarea"] = Field(
        description="choice : boutons · texte : saisie courte · commune : commune ou code postal · textarea : texte libre")
    champ: Optional[Literal["age", "commune", "demandeur", "parcours", "quartier"]] = Field(
        default=None, description="champ du schéma patient rempli par la réponse (absent pour textarea)")
    texte: str = Field(min_length=1, description="question posée, en langage courant")
    mandatory: bool = Field(description="true : réponse obligatoire ; false : la question peut être passée (réponse vide)")
    options: list[OptionConfig] = Field(default_factory=list, description="boutons (type choice) ; quartier : vide = liste du gazetteer")

    @model_validator(mode="after")
    def coherence_type(self):
        q = f"question « {self.id_question} »"
        if self.type == "textarea":
            if self.champ or self.options:
                raise ValueError(f"{q} : une question textarea n'a ni champ ni options")
            return self
        if self.champ is None:
            raise ValueError(f"{q} : indiquer le champ rempli (age, commune, demandeur, parcours, quartier)")
        if (self.type == "commune") != (self.champ == "commune"):
            raise ValueError(f"{q} : le champ commune utilise le type « commune » (et lui seul)")
        if self.type == "choice" and not self.options and self.champ != "quartier":
            raise ValueError(f"{q} : une question choice a besoin d'options")
        if self.type != "choice" and self.options:
            raise ValueError(f"{q} : seules les questions choice ont des options")
        return self

    @model_validator(mode="after")
    def valeurs_valides(self):
        if self.type == "textarea":
            return self
        permises = {
            "age": TRANCHES_AGE, "demandeur": DEMANDEURS,
            "parcours": [p for p in PARCOURS if p != "inconnu"],
        }.get(self.champ)
        for o in self.options:
            if permises is not None and o.valeur not in permises:
                raise ValueError(f"question « {self.champ} » : option {o.valeur!r} invalide ; valeurs possibles : {permises}")
            if self.champ == "commune" and not gazetteer().resoudre(o.valeur).trouve:
                raise ValueError(f"question « commune » : commune inconnue {o.valeur!r}")
            if self.champ == "quartier" and not gazetteer().resoudre(o.valeur).quartier:
                raise ValueError(f"question « quartier » : quartier inconnu {o.valeur!r}")
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
        doublons = {c for c in vus if vus.count(c) > 1}
        if doublons:
            raise ValueError(f"une seule question par champ ; en double : {sorted(doublons)}")
        absents = [c for c in CHAMPS if c not in vus]
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
                     options=[Option(valeur=o.valeur, libelle=o.libelle) for o in q.options]) for q in f.questions]


QUESTIONS: list[Question] = charger()  # ordre = ordre du fichier


def question(id_question: str | None = None, champ: str | None = None) -> Question | None:
    for q in QUESTIONS:
        if (id_question and q.id_question == id_question) or (champ and not id_question and q.champ == champ):
            return q
    return None


def premiere_textarea() -> Question | None:
    return next((q for q in QUESTIONS if q.type == "textarea"), None)


def questions_pour(p: Personne, traitees: set[str] | None = None, avec_textarea: bool = True,
                   questions: list[Question] | None = None) -> list[Question]:
    """Questions restant à poser, dans l'ordre du fichier :
    - textarea : tant qu'elle n'a pas reçu de réponse (ou n'a pas été passée) ;
    - question de champ : si le champ manque dans le schéma patient et qu'elle n'a pas été passée."""
    traitees = traitees or set()
    manquants = set(p.champs_manquants)
    out = []
    for q in questions or QUESTIONS:
        if q.id_question in traitees:
            continue
        if q.type == "textarea":
            if avec_textarea:
                out.append(q)
        elif q.champ in manquants:
            q = q.model_copy(deep=True)
            if q.champ == "quartier" and not q.options:
                q.options = [Option(valeur=x["nom"], libelle=x["nom"]) for x in gazetteer().quartiers]
            out.append(q)
    return out


def valider_reponse(champ: str, valeur: str) -> str:
    """Contrôle une réponse et la renvoie normalisée ; lève ValueError si elle est invalide."""
    valeur = valeur.strip()
    if champ == "age":
        if valeur.isdigit() and 0 <= int(valeur) <= 110:
            return str(int(valeur))
        if valeur in TRANCHES_AGE:
            return valeur
        raise ValueError(f"âge entre 0 et 110, ou une tranche parmi {TRANCHES_AGE}")
    if champ == "demandeur":
        if valeur in DEMANDEURS:
            return valeur
        raise ValueError(f"valeurs possibles : {DEMANDEURS}")
    if champ == "parcours":
        if valeur in PARCOURS and valeur != "inconnu":
            return valeur
        raise ValueError(f"valeurs possibles : {[x for x in PARCOURS if x != 'inconnu']}")
    if champ == "commune":
        gaz = gazetteer()
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
    raise ValueError("champ inconnu : age, commune, demandeur, parcours ou quartier")


if __name__ == "__main__":  # python -m app.questions : régénère config/questions.schema.json
    schema = FichierQuestions.model_json_schema(by_alias=True)
    schema["title"] = "Questions du parcours (l'ordre de la liste = l'ordre des questions)"
    (CONFIG / "questions.schema.json").write_text(json.dumps(schema, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"écrit : {CONFIG / 'questions.schema.json'}")
    charger()
    print("config/questions.json valide")
